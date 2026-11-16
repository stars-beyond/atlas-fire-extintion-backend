"""Primeiros modelos, só com o que já está em ml/registry.csv (sem download).

Precisa de numpy e scikit-learn. Para cada tarefa:
  1. separa o conjunto de teste de ml/splits.csv e não toca nele no ajuste;
  2. faz validação cruzada POR GRUPO no resto (ensaios do mesmo dia ou da
     mesma amostra nunca ficam dos dois lados);
  3. compara cada modelo com o palpite ingênuo (classe mais comum ou média);
  4. treina no resto inteiro e mede uma vez no teste.

Só entram como entrada grandezas conhecidas ANTES do ensaio (atmosfera,
combustível, geometria). Tempo de queima e temperatura de pico são resultado
do ensaio e ficam de fora, senão o modelo "acerta" olhando a resposta.

Uso:
  python3 ml/make_splits.py
  python3 ml/train_tabular.py                 # todas as tarefas
  python3 ml/train_tabular.py flex_taxa_queima cfig_autoextincao
"""
import csv
import json
import os
import sys
import warnings

import numpy as np
from sklearn.compose import ColumnTransformer
from sklearn.ensemble import RandomForestClassifier, RandomForestRegressor
from sklearn.impute import SimpleImputer
from sklearn.linear_model import LogisticRegression, Ridge
from sklearn.metrics import balanced_accuracy_score, mean_absolute_error, r2_score, roc_auc_score
from sklearn.model_selection import GroupKFold
from sklearn.pipeline import Pipeline
from sklearn.preprocessing import OneHotEncoder, StandardScaler

AQUI = os.path.dirname(os.path.abspath(__file__))
ATMOSFERA = ["o2_pct", "pressure_kpa"]

TAREFAS = {
    "flex_taxa_queima": dict(
        descricao="FLEX: taxa de queima da gota (mm²/s)", tipo="regressao",
        filtro=lambda r: r["investigation"] == "FLEX" and r["burn_rate_mm2_s"] != "",
        alvo=lambda r: float(r["burn_rate_mm2_s"]),
        num=ATMOSFERA + ["co2_pct", "he_pct", "d0_mm"], cat=["fuel"]),
    "flex_extincao": dict(
        descricao="FLEX: a chama apaga antes de a gota acabar?", tipo="classificacao",
        filtro=lambda r: r["investigation"] == "FLEX" and r["outcome"] in ("extinguished", "burned"),
        alvo=lambda r: int(r["outcome"] == "extinguished"),
        num=ATMOSFERA + ["co2_pct", "he_pct", "d0_mm"], cat=["fuel"]),
    "cfig_ignicao": dict(
        descricao="CFI-G: a chama acende?", tipo="classificacao",
        filtro=lambda r: r["investigation"] == "ACME CFI-G" and r["outcome"] != "",
        alvo=lambda r: int(r["outcome"] != "not_ignited"),
        num=ATMOSFERA, cat=["fuel", "form"]),
    "cfig_autoextincao": dict(
        descricao="CFI-G: depois de acesa, a chama se apaga sozinha?", tipo="classificacao",
        filtro=lambda r: r["investigation"] == "ACME CFI-G"
        and r["outcome"] in ("extinguished", "sustained_until_stop"),
        alvo=lambda r: int(r["outcome"] == "extinguished"),
        num=ATMOSFERA, cat=["fuel", "form"]),
    "zarm_propagacao": dict(
        descricao="ZARM: velocidade de propagação no filme de PMMA (mm/s)", tipo="regressao",
        filtro=lambda r: r["investigation"].startswith("ZARM") and r["spread_rate_mm_s"] != "",
        alvo=lambda r: float(r["spread_rate_mm_s"]),
        num=ATMOSFERA + ["flow_cm_s"], cat=[]),
    "zarm_barreira": dict(
        descricao="ZARM: a barreira de metal apaga a chama?", tipo="classificacao",
        tabela="registry_barriers.csv", grupo="trial_id",
        filtro=lambda r: r["trial_id"] != "" and r["extinction"] in ("Yes", "No"),
        alvo=lambda r: int(r["extinction"] == "Yes"),
        num=ATMOSFERA + ["gap_mm"], cat=["material"]),
}


def ler(nome):
    return list(csv.DictReader(open(os.path.join(AQUI, nome), encoding="utf-8")))


def matriz(linhas, t):
    num = np.array([[float(r[c]) if r[c] != "" else np.nan for c in t["num"]] for r in linhas], dtype=float)
    cat = np.array([[r[c] or "?" for c in t["cat"]] for r in linhas], dtype=object).reshape(len(linhas), len(t["cat"]))
    return np.hstack([num.astype(object), cat])


def modelos(t):
    n_num, n_cat = len(t["num"]), len(t["cat"])
    def pre():
        return ColumnTransformer([
            ("num", Pipeline([("imp", SimpleImputer(strategy="median")), ("esc", StandardScaler())]),
             list(range(n_num))),
            ("cat", OneHotEncoder(handle_unknown="ignore"), list(range(n_num, n_num + n_cat))),
        ])
    if t["tipo"] == "classificacao":
        return {
            "logistica": Pipeline([("pre", pre()), ("m", LogisticRegression(max_iter=2000, class_weight="balanced"))]),
            "floresta": Pipeline([("pre", pre()), ("m", RandomForestClassifier(
                n_estimators=300, min_samples_leaf=2, class_weight="balanced", random_state=0))]),
        }
    return {
        "ridge": Pipeline([("pre", pre()), ("m", Ridge(alpha=1.0))]),
        "floresta": Pipeline([("pre", pre()), ("m", RandomForestRegressor(
            n_estimators=300, min_samples_leaf=2, random_state=0))]),
    }


def metricas(tipo, y, p, ingenuo):
    """p é probabilidade (classificação) ou valor (regressão)."""
    if tipo == "classificacao":
        if len(set(y)) < 2:
            return None
        return dict(auc=round(float(roc_auc_score(y, p)), 3),
                    acuracia_balanceada=round(float(balanced_accuracy_score(y, p >= 0.5)), 3))
    return dict(r2=round(float(r2_score(y, p)), 3), mae=round(float(mean_absolute_error(y, p)), 4),
                mae_ingenuo=round(float(mean_absolute_error(y, ingenuo)), 4))


def prever(modelo, X, tipo):
    return modelo.predict_proba(X)[:, 1] if tipo == "classificacao" else modelo.predict(X)


def rodar(nome, t, splits):
    linhas = [r for r in ler(t.get("tabela", "registry.csv")) if t["filtro"](r)]
    grupos = np.array([r[t.get("grupo", "split_group")] for r in linhas])
    parte = np.array([splits.get(r["trial_id"], "train") for r in linhas])
    X, y = matriz(linhas, t), np.array([t["alvo"](r) for r in linhas], dtype=float)
    ajuste, teste = parte != "test", parte == "test"
    res = dict(descricao=t["descricao"], tipo=t["tipo"], entradas=t["num"] + t["cat"],
               n=len(linhas), n_grupos=len(set(grupos)), n_teste=int(teste.sum()), avisos=[], modelos={})
    if t["tipo"] == "classificacao":
        res["positivos"] = int(y.sum())
        if min(y.sum(), len(y) - y.sum()) < 10:
            res["avisos"].append("menos de 10 casos na classe rara: o número abaixo é instável")
    if len(linhas) < 50:
        res["avisos"].append("menos de 50 ensaios: serve para ver tendência, não para afirmar desempenho")

    Xa, ya, ga = X[ajuste], y[ajuste], grupos[ajuste]
    dobras = min(5, len(set(ga)))
    for nome_m, modelo in modelos(t).items():
        fora = np.full(len(ya), np.nan)          # previsão de cada ensaio feita sem vê-lo
        ingenuo = np.full(len(ya), np.nan)
        for tr, va in GroupKFold(n_splits=dobras).split(Xa, ya, ga):
            ingenuo[va] = ya[tr].mean()
            if t["tipo"] == "classificacao" and len(set(ya[tr])) < 2:
                fora[va] = ya[tr].mean()
                continue
            fora[va] = prever(modelo.fit(Xa[tr], ya[tr]), Xa[va], t["tipo"])
        r = dict(validacao_cruzada=metricas(t["tipo"], ya, fora, ingenuo))
        if teste.sum() >= 10:
            p = prever(modelo.fit(Xa, ya), X[teste], t["tipo"])
            r["teste"] = metricas(t["tipo"], y[teste], p, np.full(int(teste.sum()), ya.mean()))
        res["modelos"][nome_m] = r
    return res


def main(args):
    warnings.filterwarnings("ignore")
    caminho = os.path.join(AQUI, "splits.csv")
    if not os.path.exists(caminho):
        sys.exit("Rode antes: python3 ml/make_splits.py")
    splits = {r["trial_id"]: r["split"] for r in ler("splits.csv")}
    nomes = [a for a in args if not a.startswith("--")] or list(TAREFAS)
    desconhecidas = [n for n in nomes if n not in TAREFAS]
    if desconhecidas:
        sys.exit(f"Tarefa desconhecida: {desconhecidas}. Opções: {', '.join(TAREFAS)}")

    resultados = {}
    for nome in nomes:
        r = resultados[nome] = rodar(nome, TAREFAS[nome], splits)
        print(f"\n== {nome}: {r['descricao']}")
        extra = f", {r['positivos']} positivos" if "positivos" in r else ""
        print(f"   {r['n']} ensaios em {r['n_grupos']} grupos{extra}; {r['n_teste']} no teste")
        for aviso in r["avisos"]:
            print(f"   AVISO: {aviso}")
        for nome_m, m in r["modelos"].items():
            for onde in ("validacao_cruzada", "teste"):
                if m.get(onde):
                    print(f"   {nome_m:10s} {onde:18s} " + "  ".join(f"{k}={v}" for k, v in m[onde].items()))
    os.makedirs(os.path.join(AQUI, "results"), exist_ok=True)
    saida = os.path.join(AQUI, "results", "tabular.json")
    json.dump(resultados, open(saida, "w", encoding="utf-8"), indent=1, ensure_ascii=False)
    print(f"\n-> {os.path.relpath(saida)}")
    print("Referência: AUC 0,5 e acurácia balanceada 0,5 = palpite; R² 0 = prever a média.")


if __name__ == "__main__":
    main(sys.argv[1:])
