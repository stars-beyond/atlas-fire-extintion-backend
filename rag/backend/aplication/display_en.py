"""English display text for corpus metadata and data cards.

The indexed corpus (data/flame_v6/corpus.json) was generated with Portuguese warnings
and data-file cards. Editing it would change the corpus fingerprint and force a full
re-index, so the text is translated here, when sources are served to the API.
The LLM still receives the original excerpts; only what users see is translated.
"""
import re

_WARNINGS_EXACT = {
    "Extração automática não validada cientificamente. Preserve números, unidades, negações e campos ausentes; fórmulas não foram recalculadas. Confira o original antes de uso quantitativo.":
        "Automatic extraction, not scientifically validated. Keep numbers, units, negations and missing fields; formulas were not recomputed. Check the original before quantitative use.",
    "Texto matemático PDF pode perder índices, expoentes e ordem bidimensional. Candidatos têm imagem/bbox; nenhum LaTeX foi inferido nem validado. Tabelas PDF não são medições estruturadas validadas.":
        "PDF math text may lose subscripts, superscripts and 2D layout. Candidates have an image/bbox; no LaTeX was inferred or validated. PDF tables are not validated structured measurements.",
    "Extração textual por página; tabelas, gráficos e figuras não foram convertidos em dados estruturados validados.":
        "Text extracted per page; tables, charts and figures were not converted into validated structured data.",
    "XLS legado: valores armazenados e tipos preservados; parser não recupera expressões de fórmula. Fórmulas continuam no arquivo original.":
        "Legacy XLS: stored values and types preserved; the parser does not recover formula expressions. Formulas remain in the original file.",
    "Fonte histórica/arquivada: planos, recomendações e estado das missões pertencem à época do documento; não constituem orientação operacional atual.":
        "Historical/archived source: plans, recommendations and mission status belong to the document's time; they are not current operational guidance.",
    "Slides de apresentação: muitos resultados aparecem apenas em gráficos e figuras não extraídos.":
        "Presentation slides: many results appear only in charts and figures that were not extracted.",
    "Bibliografia anotada: lista e resume publicações; não contém os resultados completos dos estudos citados. Use como referência, não como evidência experimental.":
        "Annotated bibliography: lists and summarizes publications; it does not contain the full results of the cited studies. Use it as a reference, not as experimental evidence.",
    "Texto longo exportado por linha sem inferir delimitadores/colunas. Excertos do RAG são uma amostra inicial explicitamente rotulada.":
        "Long text exported line by line without inferring delimiters/columns. RAG excerpts are an explicitly labeled initial sample.",
    "Tabela de ensaios não validada independentemente. Preserve valores e campos ausentes; não corrija nem preencha automaticamente.":
        "Test table not independently validated. Keep values and missing fields; do not correct or fill them in automatically.",
    "A tabela resume ensaios com condições específicas; não é um ranking universal de risco.":
        "The table summarizes tests under specific conditions; it is not a universal risk ranking.",
    "Controles de fluxo incluem sequências textuais; há valores suspeitos de O2 final e campos 'none'. Conferir matriz e dados brutos.":
        "Flow controls include text sequences; there are suspicious final O2 values and 'none' fields. Check the matrix and raw data.",
    "Registro descritivo de investigação PSI; não é texto integral de artigo nem dataset bruto. Publicações listadas são referências bibliográficas. Objetivos, hipóteses e impactos não equivalem a resultados medidos.":
        "Descriptive PSI investigation record; not the full text of an article or a raw dataset. Listed publications are bibliographic references. Objectives, hypotheses and impacts are not measured results.",
    "Documento digitalizado com OCR: o texto corrido é legível, mas equações, índices, símbolos e alguns números podem estar corrompidos. Confira a página original antes de citar valores ou fórmulas.":
        "Scanned document with OCR: the running text is readable, but equations, subscripts, symbols and some numbers may be corrupted. Check the original page before citing values or formulas.",
    "Proposta ou conceito experimental: não relata resultados de voo validados.":
        "Experimental proposal or concept: it does not report validated flight results.",
    "O cabeçalho contém '?-g' e espessuras '037 mm'; conferir no original.":
        "The header contains '?-g' and thicknesses '037 mm'; check the original.",
    "Há células de material vazias. Não inferir que repetem o material da linha anterior.":
        "Some material cells are empty. Do not assume they repeat the previous row's material.",
    "Resumo estendido: versão curta do estudo; consulte os demais documentos do mesmo grupo.":
        "Extended abstract: short version of the study; see the other documents in the same group.",
    "Pôster: resumo visual de uma página; gráficos e figuras não foram extraídos.":
        "Poster: one-page visual summary; charts and figures were not extracted.",
    "Referências bibliográficas; os artigos citados não foram coletados integralmente por esta página.":
        "Bibliographic references; the cited articles were not collected in full from this page.",
    "Somente o resumo está disponível na NTRS; o texto completo não foi publicado no registro.":
        "Only the abstract is available on NTRS; the full text was not published in the record.",
}

_WARNINGS_PATTERNS = [
    (re.compile(r"^Esta página contém (\d+) caracteres não mapeados ou de fonte especial\. Não reconstrua fórmulas a partir desse texto; confira a imagem/PDF original\.$"),
     r"This page has \1 unmapped or special-font characters. Do not rebuild formulas from this text; check the original image/PDF."),
    (re.compile(r"^Páginas PDF sem texto extraível: (\[.*?\])\. Nenhum OCR foi aplicado; consulte o original\.$"),
     r"PDF pages without extractable text: \1. No OCR was applied; see the original."),
    (re.compile(r"^Há páginas sem texto extraível suficiente; consultar figuras e páginas originais\. Páginas: (.*)$"),
     r"Some pages lack enough extractable text; check the original figures and pages. Pages: \1"),
    (re.compile(r"^Páginas lidas com PyMuPDF porque o pypdf recusou o conteúdo comprimido: (.*)$"),
     r"Pages read with PyMuPDF because pypdf rejected the compressed content: \1"),
    (re.compile(r"^Documento do grupo de estudo '(.*)': os outros documentos do grupo descrevem a mesma investigação e não são confirmações independentes\.$"),
     r"Document from the study group '\1': the group's other documents describe the same investigation and are not independent confirmations."),
    (re.compile(r"^Cerca de (\d+)% do texto também aparece no relatório final do (.*?) \((PSI-\d+)\); não trate os dois documentos como confirmações independentes\.$"),
     r"About \1% of the text also appears in the final report of \2 (\3); do not treat the two documents as independent confirmations."),
    (re.compile(r"^Imagem do candidato na página (\d+) não renderizada \((.*?)\); consulte o PDF original\.$"),
     r"Candidate image on page \1 not rendered (\2); see the original PDF."),
]

# Data-file cards and BASS-II table rows inside the excerpts (applied line by line; also
# works on fragments, because chunks can start or end in the middle of a line).
_TEXT_PATTERNS = [
    (re.compile(r"Aba/tabela '(.*?)': (\d+) linhas exportadas; até (\d+) valores por linha\."), r"Sheet/table '\1': \2 rows exported; up to \3 values per row."),
    (re.compile(r"Primeira linha \(possíveis rótulos de coluna, não validados\): "), "First row (possible column labels, not validated): "),
    (re.compile(r"Primeira linha: "), "First row: "),
    (re.compile(r"Ficha de arquivo de dados: "), "Data file card: "),
    (re.compile(r"Investigação: (.*?)\. Pasta PSI: (.*?)\. Formato: "), r"Investigation: \1. PSI folder: \2. Format: "),
    (re.compile(r"Total exportado: (\d+) linhas em (\d+) aba\(s\)/tabela\(s\)\."), r"Total exported: \1 rows in \2 sheet(s)/table(s)."),
    (re.compile(r"Estatísticas calculadas automaticamente sobre as células exportadas; unidades e significado físico não foram inferidos\. Os valores completos estão no original e no dataset SQLite\."),
     "Statistics computed automatically over the exported cells; units and physical meaning were not inferred. The full values are in the original and in the SQLite dataset."),
    (re.compile(r"Estatísticas mostradas só para as (\d+) primeiras colunas\."), r"Statistics shown only for the first \1 columns."),
    (re.compile(r"Outras (\d+) abas não resumidas nesta ficha\."), r"\1 other sheets not summarized in this card."),
    (re.compile(r"O texto bruto deste arquivo \(só números\) não entra na busca semântica; consulte o original\."),
     "The raw text of this file (numbers only) is not part of the semantic search; see the original."),
    (re.compile(r"^(\s*- )coluna (\d+):", re.M), r"\1column \2:"),
    (re.compile(r" valores numéricos, mín\. "), " numeric values, min. "),
    (re.compile(r" máx\. "), " max. "),
    (re.compile(r"; (\d+) textos\b"), r"; \1 text values"),
    (re.compile(r"\[não informado\]"), "[not reported]"),
    # fragments cut at a chunk boundary
    (re.compile(r"linhas exportadas; até (\d+) valores por linha\."), r"rows exported; up to \1 values per row."),
    (re.compile(r"\bvalores por linha\."), "values per row."),
]


def warning_en(text):
    if not isinstance(text, str):
        return text
    if text in _WARNINGS_EXACT:
        return _WARNINGS_EXACT[text]
    for pattern, repl in _WARNINGS_PATTERNS:
        if pattern.match(text):
            return pattern.sub(repl, text)
    return text


def text_en(text):
    if not isinstance(text, str):
        return text
    for pattern, repl in _TEXT_PATTERNS:
        text = pattern.sub(repl, text)
    return text


def metadata_en(metadata):
    """Copy of a source's metadata with the user-visible Portuguese fields in English."""
    out = dict(metadata)
    if isinstance(out.get("data_warnings"), list):
        out["data_warnings"] = [warning_en(w) for w in out["data_warnings"]]
    return out
