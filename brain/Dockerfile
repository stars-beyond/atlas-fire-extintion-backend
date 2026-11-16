FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
ENV PIP_NO_CACHE_DIR=1 PYTHONUNBUFFERED=1 FASTEMBED_CACHE_PATH=/models
WORKDIR /app
COPY requirements.txt .
RUN pip install -r requirements.txt
ARG EMBED_MODEL=sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2
ENV EMBED_MODEL=${EMBED_MODEL}
# O modelo de embeddings entra na imagem: em execução nada é baixado.
RUN python -c "import os; from fastembed import TextEmbedding; TextEmbedding(os.environ['EMBED_MODEL'])"
# Com o modelo já na imagem, o serviço não consulta o Hugging Face ao subir.
ENV HF_HUB_OFFLINE=1
COPY atlas ./atlas
EXPOSE 8000
CMD ["uvicorn", "atlas.server:app", "--host", "0.0.0.0", "--port", "8000"]
