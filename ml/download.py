"""Baixa os arquivos de ml/manifest.json, por grupo, na ordem de prioridade.

Só biblioteca padrão. Pode ser interrompido e rodado de novo: arquivo já
completo é pulado, arquivo pela metade (.part) recomeça. Para antes de
estourar o orçamento e nunca deixa o disco com menos que a folga pedida.

Uso:
  python3 ml/download.py --dest /dados/ml --dry-run          # só mostra o plano
  python3 ml/download.py --dest /dados/ml                    # tudo, até 45 GB
  python3 ml/download.py --dest /dados/ml --groups zarm_meta,cfig_series
  python3 ml/download.py --dest /dados/ml --sample 2         # 2 arquivos por grupo
  python3 ml/download.py --dest /dados/ml --budget-gb 20 --min-free-gb 5

Opções: --workers N (padrão 4), --max-mb N (pula arquivos maiores que N MB).
"""
import hashlib
import json
import os
import shutil
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed

AQUI = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, AQUI)
from net import abrir  # noqa: E402

BLOCO = 1 << 20
trava = threading.Lock()


def opcao(args, nome, padrao, tipo=str):
    return tipo(args[args.index(nome) + 1]) if nome in args else padrao


def completo(caminho, item):
    """Já está no disco com o tamanho do catálogo?"""
    if not os.path.exists(caminho):
        return False
    tam = os.path.getsize(caminho)
    return tam > 0 and (item["bytes"] == 0 or tam == item["bytes"])


def baixar(item, dest, folga):
    caminho = os.path.join(dest, item["destino"])
    if completo(caminho, item):
        return "já existe", 0
    os.makedirs(os.path.dirname(caminho), exist_ok=True)
    with trava:
        livre = shutil.disk_usage(dest).free
    if livre - item["bytes"] < folga:
        return "sem espaço", 0

    parcial, md5, lidos = caminho + ".part", hashlib.md5(), 0
    for tentativa in range(3):
        try:
            with abrir(item["url"], timeout=300) as r, open(parcial, "wb") as f:
                if "text/html" in (r.headers.get("Content-Type") or ""):
                    return "FALHOU (página HTML no lugar do arquivo)", 0
                md5, lidos = hashlib.md5(), 0
                while True:
                    bloco = r.read(BLOCO)
                    if not bloco:
                        break
                    f.write(bloco)
                    md5.update(bloco)
                    lidos += len(bloco)
            break
        except Exception as e:  # rede instável: tenta de novo, depois desiste
            erro = f"{type(e).__name__}: {e}"[:120]
            time.sleep(3 * (tentativa + 1))
    else:
        if os.path.exists(parcial):
            os.remove(parcial)
        return f"FALHOU ({erro})", 0

    if item["md5"] and md5.hexdigest() != item["md5"]:
        os.remove(parcial)
        return "FALHOU (md5 não confere)", 0
    os.replace(parcial, caminho)
    if item["bytes"] and lidos != item["bytes"]:
        return f"ok, mas tamanho difere do catálogo ({lidos} vs {item['bytes']})", lidos
    return "ok", lidos


def main(args):
    if "--dest" not in args:
        sys.exit(__doc__)
    dest = os.path.abspath(opcao(args, "--dest", ""))
    orcamento = opcao(args, "--budget-gb", 45.0, float) * 1e9
    folga = opcao(args, "--min-free-gb", 3.0, float) * 1e9
    workers = opcao(args, "--workers", 4, int)
    amostra = opcao(args, "--sample", 0, int)
    max_bytes = opcao(args, "--max-mb", 0.0, float) * 1e6
    grupos = opcao(args, "--groups", "")
    grupos = set(grupos.split(",")) if grupos else None
    ensaio = "--dry-run" in args

    caminho_manifesto = opcao(args, "--manifest", os.path.join(AQUI, "manifest.json"))
    manifesto = json.load(open(caminho_manifesto, encoding="utf-8"))
    itens = manifesto["arquivos"]
    if grupos:
        desconhecidos = grupos - {i["grupo"] for i in itens}
        if desconhecidos:
            sys.exit(f"Grupo desconhecido: {', '.join(sorted(desconhecidos))}")
        itens = [i for i in itens if i["grupo"] in grupos]
    if max_bytes:
        itens = [i for i in itens if i["bytes"] <= max_bytes]
    if amostra:
        por_grupo = {}
        for i in itens:
            por_grupo.setdefault(i["grupo"], []).append(i)
        itens = [i for g in por_grupo.values() for i in g[:amostra]]

    # Corta no orçamento seguindo a prioridade; o que já está no disco conta.
    plano, soma, cortados = [], 0, {}
    for i in itens:
        if soma + i["bytes"] > orcamento:
            cortados[i["grupo"]] = cortados.get(i["grupo"], 0) + 1
            continue
        soma += i["bytes"]
        plano.append(i)

    os.makedirs(dest, exist_ok=True)
    livre = shutil.disk_usage(dest).free
    falta = sum(i["bytes"] for i in plano if not completo(os.path.join(dest, i["destino"]), i))
    print(f"Destino: {dest}")
    print(f"Plano: {len(plano)} arquivos, {soma / 1e9:.2f} GB "
          f"(falta baixar {falta / 1e9:.2f} GB; livre no disco {livre / 1e9:.1f} GB)")
    for g in dict.fromkeys(i["grupo"] for i in plano):
        do_grupo = [i for i in plano if i["grupo"] == g]
        print(f"  {g:22s} {len(do_grupo):5d} arquivos {sum(i['bytes'] for i in do_grupo) / 1e9:7.2f} GB")
    for g, n in cortados.items():
        print(f"  fora do orçamento: {n} arquivos de {g}")
    if falta + folga > livre:
        print(f"AVISO: falta espaço para o plano inteiro com {folga / 1e9:.0f} GB de folga; "
              "o download para quando a folga acabar.")
    if ensaio:
        return

    resumo, baixados, inicio = {}, 0, time.time()
    with ThreadPoolExecutor(workers) as pool:
        tarefas = {pool.submit(baixar, i, dest, folga): i for i in plano}
        for n, fut in enumerate(as_completed(tarefas), 1):
            item = tarefas[fut]
            status, lidos = fut.result()
            baixados += lidos
            chave = "falhou" if status.startswith("FALHOU") else status.split(",")[0]
            resumo.setdefault(item["grupo"], {}).setdefault(chave, []).append(item["nome"])
            if status != "já existe":
                print(f"[{n}/{len(plano)}] {status:10s} {item['bytes'] / 1e6:8.1f} MB  {item['destino']}")

    estado = dict(
        quando=time.strftime("%Y-%m-%d %H:%M:%S"), destino=dest,
        baixado_gb=round(baixados / 1e9, 3), segundos=round(time.time() - inicio),
        grupos={g: {k: len(v) for k, v in st.items()} for g, st in resumo.items()},
        falhas={g: st["falhou"] for g, st in resumo.items() if "falhou" in st},
        sem_espaco={g: st["sem espaço"] for g, st in resumo.items() if "sem espaço" in st},
    )
    json.dump(estado, open(os.path.join(dest, "download_state.json"), "w", encoding="utf-8"),
              indent=1, ensure_ascii=False)
    print(f"\nBaixado agora: {baixados / 1e9:.2f} GB em {estado['segundos']} s")
    for g, st in estado["grupos"].items():
        print(f"  {g:22s} " + ", ".join(f"{k}: {v}" for k, v in st.items()))
    if estado["falhas"] or estado["sem_espaco"]:
        print("Há falhas ou arquivos sem espaço; veja download_state.json e rode de novo.")
        sys.exit(1)


if __name__ == "__main__":
    main(sys.argv[1:])
