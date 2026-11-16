"""PDFs do PSI e recursos do desafio viram trechos com página, prontos para o índice vetorial."""
import os
import re

import fitz  # pymupdf

from . import config

# O modelo de embeddings (MiniLM) lê só 128 tokens; o resto é cortado e some da busca vetorial.
# Em inglês técnico dá ~4,4 caracteres por token, e o título vai junto no embedding (~20 tokens):
# trechos de até ~420 caracteres cabem inteiros.
CHUNK = 420
OVERLAP = 80


def clean(text):
    text = text.replace("\x00", " ")
    text = re.sub(r"-\n(?=[a-z])", "", text)
    text = re.sub(r"[ \t]+", " ", text)
    text = re.sub(r"\n{2,}", "\n", text)
    return text.strip()


def split(text):
    """Corta em blocos de ~CHUNK caracteres, de preferência em fim de frase."""
    out, i = [], 0
    while i < len(text):
        end = min(len(text), i + CHUNK)
        if end < len(text):
            cut = max(text.rfind(". ", i + CHUNK // 2, end), text.rfind("\n", i + CHUNK // 2, end))
            if cut > 0:
                end = cut + 1
        piece = text[i:end].strip()
        if len(piece) > 120:
            out.append(piece)
        if end >= len(text):
            break
        i = max(end - OVERLAP, i + 1)
    return out


def title_of(file_name, acc=None):
    name = os.path.splitext(file_name)[0]
    if acc and name.startswith(acc + "_"):
        name = name[len(acc) + 1:]
        cat, _, rest = name.partition("_")
        return f"{rest or cat}".strip(), cat
    return name.replace("_", " ").strip(), "Challenge resource"


def pdf_chunks(path, max_pages=None):
    """Gera (página, texto). Páginas sem texto (imagens, desenhos) são puladas."""
    try:
        doc = fitz.open(path)
    except Exception:
        return
    limit = max_pages or config.DOC_MAX_PAGES
    for i, page in enumerate(doc):
        if i >= limit:
            break
        try:
            text = clean(page.get_text())
        except Exception:
            continue
        letters = sum(c.isalpha() for c in text)
        if len(text) < 200 or letters < 0.5 * len(text):
            continue
        for piece in split(text):
            yield i + 1, piece
    doc.close()
