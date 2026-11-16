"""Prepara as séries temporais do ACME (CFI-G, Flame Design, s-Flame) para ML.

Só biblioteca padrão. Para cada `<ensaio>_data.csv` baixado:
  - lê o cabeçalho de configuração e os 36 canais (vazões, pressões,
    fotomultiplicadoras, termopares, radiômetros);
  - grava uma versão compacta, reamostrada em passo fixo, em
    <dest>/derived/series/<grupo>/<ensaio>.csv;
  - acrescenta uma linha em <dest>/derived/series_summary.csv com duração,
    taxa de amostragem e, por canal, valor inicial, máximo, instante do
    máximo e valor final.

ATENÇÃO ao usar como entrada de modelo: o máximo e o valor final de um
radiômetro já contêm o desfecho (a chama apagou ou não). Para prever
autoextinção, use só uma janela inicial da série reamostrada (por exemplo,
os primeiros segundos depois da ignição), nunca o resumo inteiro.

Uso:
  python3 ml/prep_series.py --dest /dados/ml
  python3 ml/prep_series.py --dest /dados/ml --groups cfig_series --step 0.2
"""
import csv
import glob
import os
import re
import sys

GRUPOS = {"cfig_series": "PSI-159", "flamedesign_series": "PSI-10", "sflame_series": "PSI-23"}


def segundos(texto):
    """'19:13:11:269' -> segundos desde a meia-noite."""
    h, m, s, ms = (int(x) for x in texto.strip().split(":"))
    return h * 3600 + m * 60 + s + ms / 1000


def ler_serie(caminho):
    with open(caminho, encoding="utf-8", errors="replace", newline="") as f:
        leitor = csv.reader(f)
        config = next(leitor)
        canais = [c.strip() for c in next(leitor)]
        tempos, valores = [], []
        for linha in leitor:
            if len(linha) != len(canais):
                continue                      # linha truncada no fim do arquivo
            try:
                t = segundos(linha[0])
                v = [float(x) for x in linha[1:]]
            except ValueError:
                continue
            tempos.append(t)
            valores.append(v)
    if not tempos:
        return None
    t0 = tempos[0]
    tempos = [t - t0 + (86400 if t < t0 else 0) for t in tempos]   # virada de meia-noite
    return dict(config=[c.strip() for c in config], canais=canais[1:], t=tempos, v=valores)


def reamostrar(serie, passo):
    """Média de cada canal em janelas de `passo` segundos."""
    saida, i, n = [], 0, len(serie["t"])
    fim = serie["t"][-1]
    k = 0
    while k * passo <= fim:
        ini, lim = k * passo, (k + 1) * passo
        soma, cont = [0.0] * len(serie["canais"]), 0
        while i < n and serie["t"][i] < lim:
            for j, x in enumerate(serie["v"][i]):
                soma[j] += x
            cont += 1
            i += 1
        if cont:
            saida.append([round(ini, 3)] + [s / cont for s in soma])
        k += 1
    return saida


def resumo(serie, janela=2.0):
    """Por canal: média dos primeiros `janela` s, máximo, instante do máximo, média dos últimos `janela` s."""
    t, v, fim = serie["t"], serie["v"], serie["t"][-1]
    campos = {}
    for j, canal in enumerate(serie["canais"]):
        col = [linha[j] for linha in v]
        ini = [x for x, ti in zip(col, t) if ti <= janela]
        ult = [x for x, ti in zip(col, t) if ti >= fim - janela]
        imax = max(range(len(col)), key=col.__getitem__)
        campos[f"{canal}__ini"] = sum(ini) / len(ini)
        campos[f"{canal}__max"] = col[imax]
        campos[f"{canal}__t_max"] = t[imax]
        campos[f"{canal}__fim"] = sum(ult) / len(ult)
    return campos


def main(args):
    if "--dest" not in args:
        sys.exit(__doc__)
    dest = os.path.abspath(args[args.index("--dest") + 1])
    passo = float(args[args.index("--step") + 1]) if "--step" in args else 0.1
    grupos = args[args.index("--groups") + 1].split(",") if "--groups" in args else list(GRUPOS)

    linhas, falhas = [], []
    for grupo in grupos:
        fonte = GRUPOS[grupo]
        arquivos = sorted(glob.glob(os.path.join(dest, grupo, "*_data.csv")))
        saida_dir = os.path.join(dest, "derived", "series", grupo)
        os.makedirs(saida_dir, exist_ok=True)
        for caminho in arquivos:
            m = re.search(r"_Raw Data_(.+)_data\.csv$", os.path.basename(caminho))
            teste = m.group(1)
            serie = ler_serie(caminho)
            if serie is None or len(serie["t"]) < 10:
                falhas.append(os.path.basename(caminho))
                continue
            with open(os.path.join(saida_dir, f"{teste}.csv"), "w", encoding="utf-8", newline="") as f:
                w = csv.writer(f)
                w.writerow(["t_s"] + serie["canais"])
                for linha in reamostrar(serie, passo):
                    w.writerow([linha[0]] + [f"{x:.6g}" for x in linha[1:]])
            dur = serie["t"][-1]
            base = dict(trial_id=f"{fonte}:{teste}", grupo=grupo, amostras=len(serie["t"]),
                        duracao_s=round(dur, 2), hz=round((len(serie["t"]) - 1) / dur, 2) if dur else "",
                        config=" | ".join(serie["config"][1:]))
            base.update({k: f"{x:.6g}" for k, x in resumo(serie).items()})
            linhas.append(base)
        print(f"{grupo}: {len(arquivos)} arquivos lidos")

    if linhas:
        colunas = list(dict.fromkeys(k for l in linhas for k in l))
        caminho = os.path.join(dest, "derived", "series_summary.csv")
        with open(caminho, "w", encoding="utf-8", newline="") as f:
            w = csv.DictWriter(f, fieldnames=colunas)
            w.writeheader()
            w.writerows(linhas)
        print(f"{len(linhas)} ensaios -> {caminho}")
    if falhas:
        print(f"{len(falhas)} arquivos sem dados utilizáveis: {falhas[:5]}")


if __name__ == "__main__":
    main(sys.argv[1:])
