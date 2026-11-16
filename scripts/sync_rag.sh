#!/bin/sh
# Atualiza rag/ a partir de um checkout do rag-Carlin ao lado deste repositório.
set -e
SRC="${1:-../rag-Carlin}"
cd "$(dirname "$0")/.."
[ -d "$SRC/backend" ] || { echo "Não achei $SRC/backend. Passe o caminho do rag-Carlin."; exit 1; }
rm -rf rag/backend
cp -R "$SRC/backend" rag/
rm -f rag/backend/aplication/.env
find rag -name "__pycache__" -prune -exec rm -rf {} +
cp "$SRC/Dockerfile" "$SRC/.dockerignore" "$SRC/requirements.lock.txt" rag/
mkdir -p rag/data/flame_v6 rag/data/formulas_v2/full
cp "$SRC/data/flame_v6/corpus.json" rag/data/flame_v6/
cp "$SRC/data/formulas_v2/full/catalog.jsonl" rag/data/formulas_v2/full/
(cd "$SRC" && git rev-parse --short HEAD) > rag/ORIGEM.txt
echo "rag/ atualizado para $(cat rag/ORIGEM.txt). Reconstrua com: docker compose up -d --build rag"
