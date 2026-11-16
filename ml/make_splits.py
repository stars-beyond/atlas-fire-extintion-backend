"""Divide os ensaios em treino/validação/teste e em 5 dobras, sempre por grupo.

Só biblioteca padrão. Lê ml/registry.csv e grava ml/splits.csv com
trial_id, split (train/val/test) e fold (0 a 4).

Ensaios do mesmo `split_group` (mesmo dia, mesma amostra, ou o mesmo ensaio
físico publicado duas vezes) caem sempre do mesmo lado. A divisão é
determinística: depende só do nome do grupo e da semente, então refazer o
registro não embaralha quem é teste.

Uso:
  python3 ml/make_splits.py            # 70/15/15, semente 2026
  python3 ml/make_splits.py --seed 7
"""
import csv
import hashlib
import os
import sys

AQUI = os.path.dirname(os.path.abspath(__file__))


def fracao(grupo, semente):
    """Número estável em [0, 1) para o grupo."""
    h = hashlib.sha256(f"{semente}:{grupo}".encode()).hexdigest()
    return int(h[:12], 16) / 16 ** 12


def main(args):
    semente = int(args[args.index("--seed") + 1]) if "--seed" in args else 2026
    linhas = list(csv.DictReader(open(os.path.join(AQUI, "registry.csv"), encoding="utf-8")))

    # Ordena os grupos de cada investigação pelo número estável e corta em 70/15/15.
    # Cortar por posição (e não por limiar fixo) garante teste não vazio mesmo
    # em investigações com poucos grupos.
    por_inv = {}
    for r in linhas:
        por_inv.setdefault(r["investigation"], set()).add(r["split_group"])
    destino = {}
    for inv, grupos in por_inv.items():
        ordenados = sorted(grupos, key=lambda g: fracao(g, semente))
        n = len(ordenados)
        n_teste = max(1, round(0.15 * n))
        n_val = max(1, round(0.15 * n))
        for i, g in enumerate(ordenados):
            split = "test" if i < n_teste else "val" if i < n_teste + n_val else "train"
            destino[g] = (split, i % 5)

    saida = os.path.join(AQUI, "splits.csv")
    with open(saida, "w", encoding="utf-8", newline="") as f:
        w = csv.writer(f)
        w.writerow(["trial_id", "investigation", "split_group", "split", "fold"])
        for r in linhas:
            split, dobra = destino[r["split_group"]]
            w.writerow([r["trial_id"], r["investigation"], r["split_group"], split, dobra])

    print(f"{'investigação':26s} {'treino':>6s} {'val':>5s} {'teste':>5s}  grupos")
    for inv in por_inv:
        c = {"train": 0, "val": 0, "test": 0}
        for r in linhas:
            if r["investigation"] == inv:
                c[destino[r["split_group"]][0]] += 1
        print(f"{inv:26s} {c['train']:6d} {c['val']:5d} {c['test']:5d}  {len(por_inv[inv])}")
    print(f"\nsemente {semente} -> {os.path.relpath(saida)}")


if __name__ == "__main__":
    main(sys.argv[1:])
