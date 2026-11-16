"""Modelos ajustados só com dados do PSI, sempre com a faixa em que valem.

FLEX: taxa de queima da gota K (mm²/s) como função linear da atmosfera inicial.
Validação cruzada separando os dias de teste, para não avaliar o modelo em gotas do mesmo dia.
"""
import numpy as np

FEATURES = ["o2", "co2", "he", "pressure_atm", "d0_mm"]


def _xy(tests, fuel):
    X, y, g = [], [], []
    for t in tests:
        m = t["metrics"]
        k = m.get("k_mm2_s")
        if t["investigation_id"] != "PSI-69" or t["fuel"] != fuel or not k or k <= 0 or not m.get("d0_mm"):
            continue
        X.append([t["o2"], t["co2"] or 0, t["he"] or 0, t["pressure_atm"], m["d0_mm"]])
        y.append(k)
        g.append(m.get("date") or "")
    return np.array(X, float), np.array(y, float), np.array(g)


def _fit(X, y):
    A = np.hstack([np.ones((len(X), 1)), X])
    coef, *_ = np.linalg.lstsq(A, y, rcond=None)
    return coef


def _cv_r2(X, y, groups, k=5):
    uniq = np.unique(groups)
    rng = np.random.default_rng(7)
    rng.shuffle(uniq)
    pred = np.zeros_like(y)
    for fold in np.array_split(uniq, min(k, len(uniq))):
        te = np.isin(groups, fold)
        c = _fit(X[~te], y[~te])
        pred[te] = np.hstack([np.ones((te.sum(), 1)), X[te]]) @ c
    ss_res = float(((y - pred) ** 2).sum())
    ss_tot = float(((y - y.mean()) ** 2).sum())
    return 1 - ss_res / ss_tot, float(np.sqrt(ss_res / len(y)))


def fit_flex(tests):
    out = []
    for fuel in ("Methanol", "Heptane"):
        X, y, g = _xy(tests, fuel)
        if len(y) < 30:
            continue
        coef = _fit(X, y)
        r2, rmse = _cv_r2(X, y, g)
        out.append({
            "id": f"flex_k_{fuel.lower()}",
            "spec": {
                "kind": "linear", "target": "k_mm2_s", "label": f"FLEX · burning rate · {fuel}",
                "fuel": fuel, "source": "PSI-69", "n": int(len(y)), "r2_cv": round(r2, 3), "rmse_cv": round(rmse, 4),
                "intercept": float(coef[0]), "features": FEATURES, "coef": [float(c) for c in coef[1:]],
                "range": {f: [float(X[:, i].min()), float(X[:, i].max())] for i, f in enumerate(FEATURES)},
                "defaults": {f: float(np.median(X[:, i])) for i, f in enumerate(FEATURES)},
                "validation": "5-fold cross-validation, folds split by test day",
            },
        })
    return out
