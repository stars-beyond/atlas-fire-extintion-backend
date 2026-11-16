"""Motor de missão (Mission Fire Engine): simula uma missão hora a hora e diz o que o acervo sabe em cada trecho.

Os módulos deste pacote são o motor do stars-beyond/mission-fire-engine (backend/mfe, commit c7d4427), copiados
sem mudar o cálculo: trajetória (orbit), balanço térmico e falhas (simulate), nível de evidência (evidence,
investigations), propagação prevista pelo Atlas-MachineL (ml_spread), bateria (battery, cabin_fire). A API do motor,
o RAG próprio e o relatório não vieram: quem fala com o site é a API Go, que repassa para service.py.
Única mudança no código copiado: cabin_fire usa np.trapz quando o numpy não tem np.trapezoid (o brain fixa
numpy 1.26 por causa do fastembed; o motor original usa numpy 2.1).

Os dados ficam em data/ (cópia de mission-fire-engine/data). Para atualizar o motor, copie de novo os módulos e
os dados do repositório de origem e anote o commit acima.
"""
