from functools import lru_cache

import numpy as np

from . import config


@lru_cache(maxsize=1)
def model():
    from fastembed import TextEmbedding
    return TextEmbedding(config.EMBED_MODEL)


def embed(texts, batch_size=64):
    """Vetores normalizados (cosseno = produto interno)."""
    if not texts:
        return np.zeros((0, config.EMBED_DIM), dtype=np.float32)
    out = np.array(list(model().embed(list(texts), batch_size=batch_size)), dtype=np.float32)
    norm = np.linalg.norm(out, axis=1, keepdims=True)
    norm[norm == 0] = 1
    return out / norm
