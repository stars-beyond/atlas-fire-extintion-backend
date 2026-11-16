# Pitch · Atlas de Extinção

## Uma frase

O Atlas de Extinção mostra, para qualquer nave, base ou rover, que evidência a NASA já tem sobre fogo naquela
atmosfera, e onde ela acaba.

## O problema

A NASA estuda fogo em microgravidade há décadas e publica tudo no PSI. São 24 investigações de combustão e mais de
13 TB de dados. Achar, comparar e interpretar isso é difícil, como o próprio enunciado diz.

E há uma distância que quase ninguém vê. A base lunar terá 30% de oxigênio ou mais, a 10,2 psi ou menos. Os tecidos e
plásticos de cabine do acervo foram testados entre 13,6% e 22,2% de O₂, todos perto de 1 atm, e nenhum em gravidade
lunar.

## A solução

Um atlas. Cada um dos 1.972 testes tabelados vira um ponto num território de oxigênio, pressão e gravidade. O usuário
monta o ambiente como quem configura um carro, faz o test drive e recebe um veredito de quatro níveis:

- **Medido**: há teste nesta condição.
- **Delimitado**: há teste em 0 g e em 1 g, e o cenário fica entre os dois.
- **Só um limite**: há teste de um lado só.
- **Terra incógnita**: não há teste.

Depois pergunta em linguagem natural e recebe a resposta com citações: o teste, o arquivo e a linha.

## O que o enunciado pede e onde está

| Pedido | No Atlas |
|---|---|
| Identificar as investigações mais relevantes para cada ambiente | Ranking por cenário, com a nota aberta em quatro partes |
| Extrair dados que apoiem modelos preditivos | Banco único de testes com origem, e o modelo de taxa de queima do FLEX com faixa de validade |
| Destacar pesquisa para controles e equipamentos | Supressão com CO₂ e hélio, detecção de fumaça, e a lista dos ensaios que fechariam mais lacunas |
| Explorar os dados por conta própria | Construtor de gráficos sobre 15 conjuntos de dados, com exportação e clique até a linha original |
| Resumir atributos e comparar investigações | Ficha de cada investigação e comparação lado a lado: o que variou e o que ficou igual |
| Aplicações na Terra | O mesmo veredito para uma sala fechada em 1 g, como salas de baterias |

## Por que é diferente

1. **Honestidade como recurso.** A resposta mais importante do sistema é "não há teste aqui". O andar da Lua aparece
   vazio no mapa.
2. **Taxa empírica em vez de probabilidade.** Onde a NASA testou, o Atlas conta o que aconteceu ("0 de 4 amostras de
   silicone sustentaram a chama"). Onde não testou, não inventa número.
3. **A IA redige, os dados decidem.** O nível de evidência sai de uma regra fixa sobre os testes. O modelo de linguagem
   não pode mudá-lo e se abstém quando a pergunta foge do acervo.
4. **Tudo tem origem.** Cada ponto abre o registro bruto, com arquivo e linha da tabela do PSI.
5. **Sai com algo na mão.** O dossiê e a matriz de próximos testes, prontos para copiar.

## Roteiro de três minutos

| Tempo | O que mostrar |
|---|---|
| 0:00 | O alarme de fumaça da Artemis II e a frase do enunciado sobre o ar da Lua. A tela abre como um configurador: "Onde a tripulação vai morar?" |
| 0:20 | Digitar no campo livre: "base lunar com roupas de algodão". O configurador se preenche sozinho: 30% de oxigênio, 10,2 psi, 0,17 g, tecido de algodão. |
| 0:35 | Mostrar que dá para trocar à mão: os cartões de habitat e de material, cada um com quantos testes da NASA existem. |
| 0:50 | **Acender.** O simulador 3D acende a amostra no palco e o selo revela: Terra incógnita. Nenhum teste da NASA nessa condição. |
| 1:15 | Aba Evidência: os testes mais parecidos. Tocar em um e ver, no mapa, o prédio com os andares da Lua e de Marte vazios, e o arquivo e a linha de origem. Trocar para o Grafo: o cenário no centro e o que os testes vizinhos têm em comum. |
| 1:45 | Aba Apagar: soltar CO₂ e ver a chama apagar em cerca de um segundo. Acender de novo e cortar a ventilação: em órbita a chama vira uma esfera e enfraquece; na Terra, não muda. |
| 2:10 | Aba Perguntar: "O que a NASA já sabe sobre este material?" e, em seguida, "Qual equação descreve a taxa de queima de uma gota?". Resposta com fontes clicáveis e a fórmula renderizada, com a página de origem. |
| 2:35 | Ficha técnica: quem estudou, o que falta testar, copiar. Fechar trocando o habitat para "Sala fechada na Terra". |

## Números para citar

- 24 investigações, 1.972 testes tabelados, 1.153 com oxigênio e pressão.
- 18.436 trechos de documentos da NASA (PSI e NTRS) no RAG da equipe, com fórmulas e fontes numeradas.
- Sólidos de cabine: 140 testes em microgravidade, entre 13,6% e 22,2% de O₂.
- FLEX: a taxa de queima média cai de 0,52 para 0,45 mm²/s com CO₂ e sobe para 0,61 com hélio.
- Saffire-II: o único par de limites (0 g e 1 g) para sólidos no acervo.

## Próximos passos

1. Extrair o desfecho dos cerca de 250 testes do BASS e do BASS-II, hoje em texto livre, com revisão humana de uma
   amostra. Isso destrava a fronteira de queima ou apaga para sólidos.
2. Achar a curva de conversão do ventilador do BASS-II para pôr o fluxo de ar como eixo.
3. Calibrar o simulador 3D da equipe (lab3d), já embutido no palco, com os testes do BASS-II.
