# Pipeline de ML — combustão em microgravidade

Como rodar o que já existe e como construir o resto. Pensado para uma máquina
com cerca de 50 GB livres.

**Estado em 15/11/2026.** As etapas 1 a 5 têm código e foram executadas (o
download só com arquivos pequenos de cada fonte). As etapas 6 a 8 são roteiro:
o código ainda não existe, porque depende de abrir os arquivos grandes.

| Etapa | Script | Estado |
|---|---|---|
| 1. Baixar os dados | `download.py` | Testado com amostras da NASA e do Zenodo; download completo não rodado |
| 2. Registro de ensaios | `build_registry.py` | Rodado: 1.668 ensaios, 760 com desfecho |
| 3. Divisão treino/teste | `make_splits.py` | Rodado |
| 4. Modelos tabulares | `train_tabular.py` | Rodado, números abaixo |
| 5. Séries temporais | `prep_series.py` | Testado em 6 arquivos |
| 6. Térmico 2D (ZARM) | repositório [Atlas-MachineL](https://github.com/stars-beyond/Atlas-MachineL); aqui, `spread_table.py` | Redes treinadas no outro repositório. `spread_table.py` as roda numa grade e grava a tabela que a API serve em `/api/predict/spread` |
| 7. Vídeo (BASS-II, BASS, SPICE) | a escrever | Roteiro |
| 8. Simulações FDS | a escrever | Roteiro |

## 0. Conteúdo do pacote

```
ml/
  PIPELINE.md, README.md, requirements.txt
  build_manifest.py   -> manifest.json   (o que baixar: 2.766 arquivos, 42,3 GB)
  download.py                            (baixa o manifesto)
  build_registry.py   -> registry.csv, registry_barriers.csv, registry_summary.json
  make_splits.py      -> splits.csv
  train_tabular.py    -> results/tabular.json
  prep_series.py      -> <dest>/derived/series/...
  net.py                                 (HTTP com certificados do sistema)
  sources/zarm/                          (tabelas pequenas do ZARM, CC-BY-NC-4.0)
data/seed/                               (tabelas NASA e desfechos do BASS-II que o registro lê)
```

Os arquivos gerados (`manifest.json`, `registry*.csv`, `splits.csv`,
`results/tabular.json`) já vêm prontos. Dá para pular direto para a etapa 1.

## Preparação

Python 3.10 ou mais novo. As etapas 1, 2, 3 e 5 usam só a biblioteca padrão.

```bash
python3 -m venv .venv
```

```bash
source .venv/bin/activate
```

```bash
pip install -r ml/requirements.txt
```

Para as etapas 6 e 7 (ainda por escrever) vão ser necessários também
`scipy` e `h5py` (ler `.mat` e `.hdf5`), `opencv-python` (vídeo) e `torch`.
Não estão no `requirements.txt` para não ocupar disco antes da hora: só o
PyTorch ocupa de 1 a 5 GB conforme a versão.

### Conta de disco (50 GB)

| Item | GB |
|---|---:|
| Dados brutos (manifesto inteiro) | 42,3 |
| Ambiente Python com PyTorch | 1 a 5 |
| Derivados (quadros reduzidos, séries reamostradas) | 2 a 5 |

Fica justo. Duas saídas, em ordem de preferência:

1. **Processar e apagar.** Reduza cada vídeo ou arquivo térmico para o tamanho
   de treino e apague o original. O `download.py` pula o que já existe, então
   baixe um grupo, processe, apague, e passe ao seguinte.
2. **Cortar pelo fim.** Os grupos estão em ordem de prioridade; com
   `--budget-gb 36` os vídeos do BASS ficam de fora.

## 1. Baixar os dados

```bash
python3 ml/download.py --dest /dados/ml --dry-run
```

```bash
python3 ml/download.py --dest /dados/ml --sample 2
```

```bash
python3 ml/download.py --dest /dados/ml
```

O primeiro mostra o plano. O segundo baixa 2 arquivos de cada grupo: abra-os
antes de baixar tudo. O terceiro baixa os 42,3 GB. Troque `/dados/ml` pela
pasta de destino.

Opções: `--groups a,b` (só alguns grupos), `--budget-gb N` (padrão 45),
`--min-free-gb N` (padrão 3), `--workers N` (padrão 4), `--max-mb N`.
Interrompeu? Rode de novo. Falhas ficam em `<dest>/download_state.json`.

| Prioridade | Grupo | Arquivos | GB | Conteúdo | Rótulo no registro |
|---:|---|---:|---:|---|---|
| 1 | `zarm_meta` | 9 | 0,0 | Índices e tabelas das figuras do ZARM | sim |
| 2 | `spice_tabelas` | 33 | 0,03 | Planilhas de voo do SPICE (comprimento de chama) | a ligar |
| 3 | `cfig_series` | 678 | 1,47 | Séries por chama do CFI-G, 36 canais a 125 Hz | sim (327 chamas) |
| 4 | `zarm_barreiras` | 30 | 3,84 | Infravermelho, PMMA com barreiras de metal | sim |
| 5 | `zarm_vaos_ar` | 26 | 7,23 | Infravermelho, PMMA com vãos de ar, fluxo 10–20 cm/s | a extrair |
| 6 | `fds_surrogate` | 1 | 0,39 | 500 simulações FDS (**simulação**) | no próprio zip |
| 7 | `flamedesign_series` | 700 | 1,28 | Séries por chama do Flame Design | não |
| 8 | `sflame_series` | 746 | 0,85 | Séries por chama do s-Flame | não |
| 9 | `bassii_video` | 244 | 17,26 | Um vídeo por teste do BASS-II | sim (123 testes) |
| 10 | `tabelas_extra` | 60 | 0,16 | Matrizes do BASS, tabelas do Saffire, resumos por ensaio do CFI | a extrair |
| 11 | `bassii_extra` | 179 | 0,57 | Segunda vista dos testes do BASS-II (fotos em sequência, folhas de quadros) | sim |
| 12 | `spice_video` | 18 | 1,16 | Vídeos das chamas do SPICE | a ligar |
| 13 | `saffire1_imagens` | 13 | 1,43 | Saffire-I: fotos corrigidas, amostra grande (um experimento) | não |
| 14 | `bass_video` | 29 | 6,60 | BASS: 4 das 12 sessões, inteiras | a extrair |

Os grupos 10 a 14 são a segunda leva (+10 GB). No `bass_video` as sessões
entram inteiras e em ordem de data, não os vídeos menores primeiro: escolher
por tamanho favoreceria testes que apagaram rápido e enviesaria o conjunto.
As outras 8 sessões do BASS (cerca de 16 GB) não cabem.

Para refazer a lista (por exemplo, com outro corte do BASS):

```bash
python3 ml/build_manifest.py --live
```

## 2. Registro de ensaios

Já vem pronto em `ml/registry.csv`. Para refazer:

```bash
python3 ml/build_registry.py
```

Uma linha por ensaio, com condições (`o2_pct`, `pressure_kpa`, `flow_cm_s`,
`gravity_g`), `outcome`, alvos numéricos, `split_group`, `files` (caminhos
relativos ao `--dest`), `license` e `provenance`.

| Investigação | Ensaios | Com desfecho | Com arquivos |
|---|---:|---:|---:|
| ZARM barreiras de metal | 31 | 30 | 30 |
| ZARM vãos de ar | 26 | 0 | 26 |
| BASS-II | 129 | 123 | 125 |
| FLEX | 274 | 274 | 0 |
| ACME CFI-G | 333 | 333 | 327 |
| ACME Flame Design | 327 | 0 | 307 |
| SPICE | 469 | 0 | 0 |
| CFI | 79 | 0 | 0 |

Regras que o resto do pipeline assume:

- `outcome` vazio quer dizer "a fonte não diz", não "queimou".
- No BASS-II, `quench` e `blowoff` (em `outcome_raw`) são testes de limite: a
  chama se manteve na condição inicial e apagou quando o ar foi alterado.
- `registry_barriers.csv` tem 45 barreiras medidas. As 4 linhas `Critical`
  não correspondem a ensaio nenhum e não são medição.
- Pressão de 1 atm no BASS-II e ar de cabine no SPICE são suposições,
  marcadas em `notes`.

## 3. Divisão treino/validação/teste

```bash
python3 ml/make_splits.py
```

Grava `ml/splits.csv` (70/15/15 e 5 dobras). A divisão é por `split_group`:
ensaios do mesmo dia, da mesma amostra ou o mesmo ensaio físico publicado em
dois registros ficam sempre do mesmo lado. É determinística.

**Nunca sorteie quadros ou linhas soltas.** Dois quadros vizinhos do mesmo
ensaio são quase iguais; se um cai no treino e o outro no teste, o modelo
parece ótimo e não aprendeu nada.

## 4. Modelos tabulares (não precisa de download)

```bash
python3 ml/train_tabular.py
```

Roda seis tarefas com validação cruzada por grupo, compara com o palpite
ingênuo e mede uma vez no teste. Resultado em `ml/results/tabular.json`.

Números obtidos em 15/11/2026 (validação cruzada por grupo, melhor modelo):

| Tarefa | Ensaios | Resultado | Leitura |
|---|---:|---|---|
| FLEX: taxa de queima | 258 | R² 0,67; erro 0,053 contra 0,113 do palpite | Funciona |
| FLEX: a chama apaga antes do fim? | 213 | AUC 0,79 | Funciona, classes desbalanceadas (186 de 213) |
| CFI-G: autoextinção | 263 | AUC 0,67 a 0,81 | Instável: no teste os dois modelos trocam de ordem (0,78 e 0,61) |
| CFI-G: acende? | 333 | AUC 0,51 | Atmosfera e combustível não bastam: é palpite |
| ZARM: velocidade de propagação | 22 | R² 0,82 | Só tendência: 22 ensaios |
| ZARM: barreira apaga? | 45 | AUC 0,54 a 0,68 | Só 5 casos positivos: não dá para afirmar nada |

Referência: AUC 0,5 é palpite; R² 0 é prever a média.

Só entram grandezas conhecidas antes do ensaio. Tempo de queima e
temperatura de pico são resultado e ficam de fora de propósito.

O CFI-G tem só 23 grupos (dias), por isso a medida oscila. É onde as séries
da etapa 5 devem ajudar.

## 5. Séries temporais do ACME

Depois de baixar `cfig_series` (e, se quiser, `flamedesign_series` e
`sflame_series`):

```bash
python3 ml/prep_series.py --dest /dados/ml
```

Para cada ensaio grava a série reamostrada (padrão 0,1 s) em
`<dest>/derived/series/<grupo>/<ensaio>.csv` e uma linha de resumo em
`<dest>/derived/series_summary.csv`. Cada arquivo original tem cerca de 40 s
a 125 Hz e 36 canais.

**Cuidado com vazamento.** No teste, o máximo do radiômetro foi 4,78 nas
chamas que acenderam e −0,27 nas que não acenderam: o resumo da série inteira
já contém a resposta. Para prever autoextinção:

1. Ache o instante de ignição (subida do radiômetro ou da fotomultiplicadora).
2. Use só uma janela curta depois dela (por exemplo, 2 a 5 s) como entrada.
3. O alvo continua vindo do `registry.csv` (`outcome`).
4. Compare com o modelo da etapa 4, que não vê a série.

Modelos sugeridos, do mais simples ao mais caro: estatísticas da janela em
floresta aleatória; depois uma rede 1D convolucional pequena sobre a janela.

## 6. Térmico 2D do ZARM (a escrever)

Objetivo: dado o mapa de temperatura dos últimos quadros, prever o próximo;
e medir velocidade de propagação e travessia de vãos.

Leitura dos arquivos (**não testado aqui**; a primeira coisa a fazer é abrir
um arquivo de cada tipo):

```python
from scipy.io import loadmat
quadros = loadmat("93.mat")["data_IR_undist"]   # (x, y, tempo), em °C, segundo os autores

import h5py
with h5py.File("exp6.hdf5") as f:
    print(list(f.keys()))                       # o nome do conjunto não foi conferido
```

Passos:

1. **Conferir formato:** dimensões, número de quadros (esperado: cerca de 74
   por ensaio a 8,33 quadros/s), faixa de temperatura, quadros antes da
   ignição.
2. **Reduzir:** recortar na região da amostra (`Sample_Shapes.csv` dá as
   bordas) e reduzir a resolução. Salvar como `.npy` e apagar o original se
   faltar disco.
3. **Rótulos que faltam:** posição da frente (primeiro pixel acima de um
   limiar de temperatura, ao longo do eixo de propagação) → velocidade; e,
   nos 26 ensaios com vãos de ar, se a frente reaparece depois do vão.
4. **Baselines obrigatórios:** persistência (próximo = último) e translação
   (desloca o último quadro pela velocidade medida). A chama anda de 1,5 a 4
   pixels por quadro em regime quase estável, então a translação é difícil de
   bater. Se a rede não ganhar dela, diga isso.
5. **Rede:** U-Net pequena, entrada com 4 ou 5 quadros mais um canal de
   máscara (combustível/barreira) e as condições do ensaio.
6. **Medir:** erro em °C por horizonte (1 passo e vários passos realimentados),
   erro na posição da frente, sempre em ensaios inteiros do conjunto de teste.

Limites: um só material (PMMA de 0,075 mm), 4 atmosferas com O₂ e pressão
amarrados, 3 níveis de fluxo. Quatro ensaios aparecem nos dois registros
(`same_run_as`); já estão no mesmo grupo de divisão.

## 7. Vídeo (a escrever)

BASS-II é o conjunto com rótulo (123 testes); BASS e SPICE precisam antes de
ligação com tabelas.

1. **Reduzir cedo:** extrair quadros a 2–5 por segundo, em resolução baixa,
   e apagar o vídeo. É o que faz os 25 GB de vídeo caberem.
2. **Frente de chama:** limiar de brilho no canal mais sensível à chama,
   posição ao longo da amostra por quadro → curva posição × tempo → velocidade.
3. **Conferir:** comparar a curva com a observação escrita do relatório
   (`data/seed/outcomes/PSI-25.json`) em uma dúzia de testes antes de confiar.
4. **BASS:** ligar cada vídeo ao teste pela data e hora do nome do arquivo e
   pela planilha `As Run_Test Matrix.xlsx` (grupo `tabelas_extra`; usar só a
   aba "as run matrix", cabeçalho na linha 3).
5. **SPICE:** ligar `Spice_Flight_Flamelength.xls` às 469 linhas do registro.

Vídeo de luz visível não é temperatura. O que sai daqui é geometria e
velocidade da chama, não campo térmico.

## 8. Simulações FDS (a escrever)

`fds_surrogate` traz 500 simulações de propagação com parâmetros de entrada e
saídas 1D e 2D, feitas para treinar modelos substitutos. O zip não foi
aberto: o primeiro passo é listar quais parâmetros variam (se incluem
propriedades do material) e em que gravidade foram rodadas.

Regras: marcar tudo como `simulated=yes`, nunca somar com medições na mesma
métrica, e avaliar qualquer modelo treinado em simulação contra ensaios reais
separadamente.

## Regras de validação (valem para todas as etapas)

1. Divisão por grupo, nunca por linha ou quadro.
2. Sempre um baseline ingênuo ao lado do modelo.
3. Entrada só com o que se conhece antes do instante previsto.
4. Não misturar sólidos, gotas e gás no mesmo modelo sem tratar a diferença.
5. Condição fora da faixa treinada: avisar, não responder.
6. Com menos de 50 ensaios ou menos de 10 casos na classe rara, reportar como
   tendência.
7. Modelo treinado só em PMMA não diz nada sobre outro material.

## Licenças

- NASA PSI: CC0-1.0.
- ZARM (Zenodo 18479713 e 14959942): CC-BY-NC-4.0. Uso não comercial, com
  atribuição a Ries, Krieger, Meyer e Eigenbrod (ZARM, Universidade de Bremen).
- FDS (Zenodo 21314513): CC-BY-4.0.
