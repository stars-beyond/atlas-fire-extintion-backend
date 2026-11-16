#!/usr/bin/env python3
"""Converte um STL binário (por exemplo um terreno do NASA 3D Resources) num GLB leve para a web.

O STL repete cada vértice em todos os triângulos que o usam. Aqui os vértices são unidos, as normais
suaves são calculadas e o modelo sai com Y para cima (como o three.js espera), centrado em X e Z e
apoiado em Y = 0. Com --top fica só a superfície de cima: os modelos de terreno para impressão 3D vêm
como um bloco, e a base e as paredes do bloco não interessam na cena.

Uso (só biblioteca padrão):
    python3 scripts/stl_to_glb.py "Apollo 14 - Landing Site.stl" ../atlas-fire-extintion-front/public/models/apollo14.glb --top
"""
import argparse
import json
import math
import struct
import sys


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("src")
    ap.add_argument("dst")
    ap.add_argument("--top", action="store_true", help="mantém só a superfície de cima (terrenos em bloco)")
    ap.add_argument("--name", default="modelo")
    args = ap.parse_args()

    raw = open(args.src, "rb").read()
    n = struct.unpack_from("<I", raw, 80)[0]
    if len(raw) != 84 + 50 * n:
        sys.exit("não é um STL binário")
    tris = [struct.unpack_from("<9f", raw, 84 + 50 * i + 12) for i in range(n)]
    zmin = min(min(t[2], t[5], t[8]) for t in tris)

    index, pos, idx = {}, [], []
    kept = 0
    for t in tris:
        a, b, c = t[0:3], t[3:6], t[6:9]
        ux, uy, uz = b[0] - a[0], b[1] - a[1], b[2] - a[2]
        vx, vy, vz = c[0] - a[0], c[1] - a[1], c[2] - a[2]
        nx, ny, nz = uy * vz - uz * vy, uz * vx - ux * vz, ux * vy - uy * vx
        ln = math.sqrt(nx * nx + ny * ny + nz * nz)
        if ln < 1e-12:
            continue
        if args.top:
            # fora as paredes (normal horizontal) e a base (tudo na cota mínima); a face de cima fica virada para cima
            if abs(nz) / ln < 0.02 or max(a[2], b[2], c[2]) <= zmin + 1e-4:
                continue
            if nz < 0:
                b, c = c, b
        kept += 1
        for p in (a, b, c):
            key = (round(p[0], 3), round(p[1], 3), round(p[2], 3))
            i = index.get(key)
            if i is None:
                i = index[key] = len(pos)
                pos.append(p)
            idx.append(i)

    # Z para cima (STL) -> Y para cima (glTF): (x, y, z) -> (x, z, -y); centra em X/Z e apoia em Y = 0
    xs = [p[0] for p in pos]; ys = [p[1] for p in pos]; zs = [p[2] for p in pos]
    cx, cy, z0 = (min(xs) + max(xs)) / 2, (min(ys) + max(ys)) / 2, min(zs)
    P = [(p[0] - cx, p[2] - z0, -(p[1] - cy)) for p in pos]

    N = [[0.0, 0.0, 0.0] for _ in P]
    for k in range(0, len(idx), 3):
        ia, ib, ic = idx[k], idx[k + 1], idx[k + 2]
        a, b, c = P[ia], P[ib], P[ic]
        ux, uy, uz = b[0] - a[0], b[1] - a[1], b[2] - a[2]
        vx, vy, vz = c[0] - a[0], c[1] - a[1], c[2] - a[2]
        nx, ny, nz = uy * vz - uz * vy, uz * vx - ux * vz, ux * vy - uy * vx   # peso pela área
        for i in (ia, ib, ic):
            N[i][0] += nx; N[i][1] += ny; N[i][2] += nz
    for v in N:
        ln = math.sqrt(v[0] ** 2 + v[1] ** 2 + v[2] ** 2) or 1.0
        v[0] /= ln; v[1] /= ln; v[2] /= ln

    bpos = b"".join(struct.pack("<3f", *p) for p in P)
    bnor = b"".join(struct.pack("<3f", *v) for v in N)
    bidx = struct.pack(f"<{len(idx)}I", *idx)
    mn = [min(p[a] for p in P) for a in range(3)]; mx = [max(p[a] for p in P) for a in range(3)]
    gltf = {
        "asset": {"version": "2.0", "generator": "atlas stl_to_glb.py"},
        "scene": 0, "scenes": [{"nodes": [0]}], "nodes": [{"mesh": 0, "name": args.name}],
        "meshes": [{"name": args.name, "primitives": [{"attributes": {"POSITION": 0, "NORMAL": 1}, "indices": 2}]}],
        "buffers": [{"byteLength": len(bpos) + len(bnor) + len(bidx)}],
        "bufferViews": [
            {"buffer": 0, "byteOffset": 0, "byteLength": len(bpos), "target": 34962},
            {"buffer": 0, "byteOffset": len(bpos), "byteLength": len(bnor), "target": 34962},
            {"buffer": 0, "byteOffset": len(bpos) + len(bnor), "byteLength": len(bidx), "target": 34963},
        ],
        "accessors": [
            {"bufferView": 0, "componentType": 5126, "count": len(P), "type": "VEC3", "min": mn, "max": mx},
            {"bufferView": 1, "componentType": 5126, "count": len(P), "type": "VEC3"},
            {"bufferView": 2, "componentType": 5125, "count": len(idx), "type": "SCALAR"},
        ],
    }
    js = json.dumps(gltf, separators=(",", ":")).encode()
    js += b" " * (-len(js) % 4)
    bin_ = bpos + bnor + bidx
    bin_ += b"\0" * (-len(bin_) % 4)
    out = struct.pack("<4sII", b"glTF", 2, 12 + 8 + len(js) + 8 + len(bin_))
    out += struct.pack("<I4s", len(js), b"JSON") + js + struct.pack("<I4s", len(bin_), b"BIN\0") + bin_
    open(args.dst, "wb").write(out)
    print(f"{n} triângulos no STL, {kept} mantidos, {len(P)} vértices; tamanho {[round(mx[a] - mn[a], 1) for a in range(3)]}; {len(out) / 1e6:.2f} MB")


if __name__ == "__main__":
    main()
