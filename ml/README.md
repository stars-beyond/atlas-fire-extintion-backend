# ml/ — dados e primeiros modelos

O passo a passo completo está em [PIPELINE.md](PIPELINE.md).

| Arquivo | O que faz | Precisa de |
|---|---|---|
| `build_manifest.py` | Gera `manifest.json`: o que baixar, de onde, tamanho, licença | só Python |
| `download.py` | Baixa o manifesto por grupo, retomável, dentro do orçamento de disco | só Python |
| `build_registry.py` | Gera `registry.csv`: uma linha por ensaio | só Python |
| `make_splits.py` | Gera `splits.csv`: treino/validação/teste por grupo | só Python |
| `train_tabular.py` | Primeiros modelos sobre o registro | numpy, scikit-learn |
| `prep_series.py` | Reamostra e resume as séries do ACME | só Python |
| `net.py` | HTTP com certificados do sistema | só Python |
| `spread_table.py` | Roda as redes do [Atlas-MachineL](https://github.com/stars-beyond/Atlas-MachineL) numa grade de O₂ × pressão × fluxo e grava `data/seed/sim/spread.json`, que a API serve em `/api/predict/spread` | numpy, torch e um checkout do Atlas-MachineL |
| `export_thermal.py` | Exporta as redes do Atlas-MachineL para ONNX em `brain/atlas/thermalcam/models/` (confere cada uma contra o torch), com o ensaio de exemplo: é o que a câmera térmica (`/api/predict/thermal`) roda | numpy, torch, onnx, onnxruntime e um checkout do Atlas-MachineL |

Em números: 2.766 arquivos e 42,3 GB no manifesto; 1.668 ensaios no registro,
760 com desfecho.

Início rápido, sem baixar nada:

```bash
python3 ml/make_splits.py
```

```bash
python3 ml/train_tabular.py
```

Na máquina de treino:

```bash
python3 ml/download.py --dest /dados/ml --dry-run
```

Licenças: NASA PSI é CC0-1.0; ZARM é CC-BY-NC-4.0 (uso não comercial, com
atribuição); o conjunto FDS é CC-BY-4.0.
