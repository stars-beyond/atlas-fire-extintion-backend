from datetime import datetime, timedelta
import uuid
from sqlalchemy import func
from sqlalchemy.orm import selectinload
from . import models

def create_conversation(db):
    conversation = models.Conversation()
    db.add(conversation)
    db.commit()
    db.refresh(conversation)
    return conversation

def get_conversation(db, conversation_id):
    return db.query(models.Conversation).options(selectinload(models.Conversation.messages)).filter_by(id=conversation_id).first()

def list_conversations(db):
    return db.query(models.Conversation.id, models.Conversation.title, func.max(models.Message.timestamp).label("timestamp")).outerjoin(models.Message).group_by(models.Conversation.id, models.Conversation.title).order_by(func.max(models.Message.timestamp).desc(), models.Conversation.id).all()

def assistant_id(request_id):
    return str(uuid.uuid5(uuid.NAMESPACE_URL, "flame-answer:" + request_id))

def save_turn(db, conversation, question, answer, sources, request):
    """Both halves of a successful turn and its evidence are one transaction."""
    timestamp = datetime.utcnow()
    user = models.Message(id=request.id, content=question, role="user", conversation_id=conversation.id, timestamp=timestamp)
    assistant = models.Message(id=assistant_id(request.id), content=answer, role="assistant", sources=sources, conversation_id=conversation.id, timestamp=timestamp + timedelta(microseconds=1))
    request.status = "completed"
    request.detail = None
    if not conversation.title:
        conversation.title = question[:80]
    db.add_all([user, assistant])
    try:
        db.commit()
    except Exception:
        db.rollback()
        raise
    db.refresh(user)
    db.refresh(assistant)
    return user, assistant
