# RAG da equipe (rag-Carlin)

Motor de perguntas e respostas do Atlas. É o backend do repositório
[stars-beyond/rag-Carlin](https://github.com/stars-beyond/rag-Carlin), copiado sem alterações:
`backend/`, `Dockerfile`, `requirements.lock.txt` e os dois catálogos em `data/`.
O commit de origem está em `ORIGEM.txt`.

Ele responde sobre a coleção `flame_freefall_2026_v6` (18.436 trechos), com fontes numeradas e histórico de
conversa. No Atlas a coleção fica no pgvector, não no Qdrant: a camada `atlas_pg/` (fora de `backend/`, para o
`sync_rag.sh` não apagar) troca o armazenamento, indexa sozinha na subida (`rag-index`) e soma ao corpus os PDFs do PSI
que só o Atlas tem (`atlas_pg/bridge.py`). O catálogo de fórmulas transcritas por GPT fica desligado. Precisa de `OPENAI_API_KEY`
no `.env` da raiz. Sem ela o serviço sobe, mas fica indisponível, e o Atlas usa a busca local.

Para atualizar a partir do repositório do Carlin, com os dois lado a lado:

```bash
scripts/sync_rag.sh
```
