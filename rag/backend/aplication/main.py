import json
import os
from contextlib import asynccontextmanager, contextmanager
from threading import Lock
from weakref import WeakValueDictionary
from fastapi import Depends, FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse, StreamingResponse
from sqlalchemy.orm import Session
from . import crud, models, schemas, services
from .display_en import metadata_en, text_en
from .database import SessionLocal, engine, get_db
from backend.flame.config import settings

_locks = WeakValueDictionary()
_locks_guard = Lock()

@contextmanager
def conversation_lock(conversation_id):
    # The local application runs one API worker. Serialize turns across its threads.
    with _locks_guard:
        lock = _locks.setdefault(conversation_id, Lock())
    with lock:
        yield

@asynccontextmanager
async def lifespan(app):
    models.Base.metadata.create_all(bind=engine)
    # A previous process may have stopped after reserving, before committing a turn.
    from sqlalchemy.orm import Session as DbSession
    with DbSession(engine) as db:
        db.query(models.TurnRequest).filter_by(status="pending").update({"status":"failed", "detail":"Server restarted. Please resend the question."})
        db.commit()
    services.setup_rag_pipeline()
    yield
    services.close_pipeline()

app = FastAPI(title="CosmoEX Flame — Microgravity Combustion", lifespan=lifespan)
app.add_middleware(CORSMiddleware, allow_origins=os.getenv("CORS_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173,http://localhost:8080,http://127.0.0.1:8080").split(","), allow_methods=["GET", "POST"], allow_headers=["Content-Type"])

def message_out(message):
    sources = []
    for n, item in enumerate(message.sources or [], 1):
        source = {**metadata_en(item), "citation": item.get("citation") or n}
        if isinstance(source.get("excerpt"), str):
            source["excerpt"] = text_en(source["excerpt"])
        if source.get("local_file"):
            source["local_url"] = f'/documents/{source["document_id"]}'
            if source.get("content_sha256"):
                source["local_url"] += "?revision=" + source["content_sha256"]
            if source.get("page"):
                source["local_url"] += f'#page={source["page"]}'
        sources.append(source)
    scores = [s["retrieval_score"] for s in sources if s.get("retrieval_score") is not None]
    return schemas.Message(id=message.id, role=message.role, content=message.content, timestamp=message.timestamp, sources=sources, confidence=sum(scores) / len(scores) if scores else None)

@app.get("/conversations/", response_model=list[schemas.ConversationSummary])
def list_conversations(db: Session = Depends(get_db)):
    return crud.list_conversations(db)

@app.post("/conversations/", response_model=schemas.Conversation, status_code=201)
def start_new_conversation(db: Session = Depends(get_db)):
    conversation = crud.create_conversation(db)
    return {"id": conversation.id, "title": conversation.title, "messages": []}

@app.get("/conversations/{conversation_id}", response_model=schemas.Conversation)
def get_conversation_history(conversation_id: str, db: Session = Depends(get_db)):
    conversation = crud.get_conversation(db, conversation_id)
    if conversation is None:
        raise HTTPException(404, "Conversation not found")
    return {"id": conversation.id, "title": conversation.title, "messages": [message_out(m) for m in conversation.messages]}

@app.post("/conversations/{conversation_id}/messages", response_model=schemas.MessageResponse)
def post_message(conversation_id: str, message_in: schemas.MessageSend, db: Session = Depends(get_db)):
    with conversation_lock(conversation_id):
        conversation = crud.get_conversation(db, conversation_id)
        if conversation is None:
            raise HTTPException(404, "Conversation not found")
        request_id = str(message_in.request_id)
        request = db.get(models.TurnRequest, request_id)
        if request:
            if request.conversation_id != conversation_id or request.content != message_in.content:
                raise HTTPException(409, "Identifier already used for another question.")
            if request.status == "completed":
                return {"user_message":message_out(db.get(models.Message, request_id)), "ai_message":message_out(db.get(models.Message, crud.assistant_id(request_id)))}
            if request.status == "pending":
                raise HTTPException(409, "The question is still being processed. Wait and try to retrieve the answer.")
            request.status, request.detail = "pending", None
        else:
            request = models.TurnRequest(id=request_id, conversation_id=conversation_id, content=message_in.content, status="pending")
            db.add(request)
        db.commit()  # Reserve before any external call; successful messages commit together.
        try:
            result = services.get_ai_response(message_in.content, conversation.messages)
            user, assistant = crud.save_turn(db, conversation, message_in.content, result["answer"], result["sources"], request)
        except (services.RagUnavailable, services.GenerationError) as error:
            db.rollback()
            request.status, request.detail = "failed", str(error)
            db.commit()
            raise HTTPException(503 if isinstance(error, services.RagUnavailable) else 502, str(error)) from error
        except Exception:
            db.rollback()
            request.status, request.detail = "failed", "Could not save the answer."
            db.commit()
            raise
        return {"user_message": message_out(user), "ai_message": message_out(assistant)}

def _sse(event, data):
    return f"event: {event}\ndata: {json.dumps(data, ensure_ascii=False, default=str)}\n\n"

@app.post("/conversations/{conversation_id}/messages/stream")
def post_message_stream(conversation_id: str, message_in: schemas.MessageSend):
    """Como POST /messages, mas em Server-Sent Events: "texto" com pedaços da resposta enquanto o modelo escreve,
    "pronto" com as mensagens salvas (mesmo formato de /messages) ou "erro". A resposta só é salva depois de validada."""
    def events():
        # sessão própria: a do Depends fecharia antes do fim do stream
        with conversation_lock(conversation_id), SessionLocal() as db:
            conversation = crud.get_conversation(db, conversation_id)
            if conversation is None:
                yield _sse("erro", {"status": 404, "detail": "Conversation not found"})
                return
            request_id = str(message_in.request_id)
            request = db.get(models.TurnRequest, request_id)
            if request:
                yield _sse("erro", {"status": 409, "detail": "Identifier already used."})
                return
            request = models.TurnRequest(id=request_id, conversation_id=conversation_id, content=message_in.content, status="pending")
            db.add(request)
            db.commit()
            try:
                result = None
                for kind, value in services.stream_ai_response(message_in.content, conversation.messages):
                    if kind == "texto":
                        yield _sse("texto", {"t": value})
                    else:
                        result = value
                user, assistant = crud.save_turn(db, conversation, message_in.content, result["answer"], result["sources"], request)
                yield _sse("pronto", {"user_message": message_out(user).model_dump(mode="json"), "ai_message": message_out(assistant).model_dump(mode="json")})
            except (services.RagUnavailable, services.GenerationError) as error:
                db.rollback()
                request.status, request.detail = "failed", str(error)
                db.commit()
                yield _sse("erro", {"status": 503 if isinstance(error, services.RagUnavailable) else 502, "detail": str(error)})
            except Exception:
                db.rollback()
                request.status, request.detail = "failed", "Could not save the answer."
                db.commit()
                yield _sse("erro", {"status": 500, "detail": "Could not save the answer."})
    return StreamingResponse(events(), media_type="text/event-stream", headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"})

@app.get("/conversations/{conversation_id}/requests/{request_id}")
def get_request_status(conversation_id: str, request_id: str, db: Session = Depends(get_db)):
    request = db.get(models.TurnRequest, request_id)
    if not request or request.conversation_id != conversation_id:
        raise HTTPException(404, "Request not found")
    return {"status":request.status, "detail":request.detail}

@app.get("/documents/{document_id}")
def get_document(document_id: str, revision: str | None = None):
    """Serve registered local sources, never a path supplied by the caller."""
    corpus_dir = settings()["corpus"].parent
    manifest = corpus_dir / "local_files.json"
    registry = json.loads(manifest.read_text(encoding="utf-8")) if manifest.is_file() else {}
    relative = registry.get(document_id + ":" + revision) if revision else registry.get(document_id)
    if not relative and revision:
        # Compatibility with a registry produced before immutable versioned storage.
        import hashlib
        old = registry.get(document_id)
        candidate = (corpus_dir / old).resolve() if old else None
        if candidate and candidate.is_relative_to(corpus_dir.resolve()) and candidate.is_file() and hashlib.sha256(candidate.read_bytes()).hexdigest() == revision:
            relative = old
    if not relative:
        raise HTTPException(404, "Document not found")
    path = (corpus_dir / relative).resolve()
    if not path.is_relative_to(corpus_dir.resolve()) or not path.is_file():
        raise HTTPException(404, "Document not found")
    media_type = {".pdf": "application/pdf", ".csv": "text/csv", ".json": "application/json", ".txt": "text/plain"}.get(path.suffix.lower(), "application/octet-stream")
    return FileResponse(path, media_type=media_type, filename=path.name, content_disposition_type="inline")

@app.get("/")
@app.get("/health")
def health_check():
    return {"status": "ok", "rag": services.status()}
