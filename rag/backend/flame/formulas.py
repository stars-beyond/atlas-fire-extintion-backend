"""Find mathematical pages in the validated corpus without rewriting its vectors.

These are lexical context candidates, not a count of validated physical laws.
Only version-pinned visual transcriptions may supply reconstructed LaTeX.
"""
import json
import re
import unicodedata
from collections import Counter
from pathlib import Path
from types import SimpleNamespace


def normalized(text):
    value = ''.join(c for c in unicodedata.normalize('NFKD', text.casefold())
                    if not unicodedata.combining(c))
    return ' '.join(value.split())


STOP = set('a o as os de da do das dos em no na nos nas e ou um uma que quais qual como para por com foi foram sao ser tem se ao the a an of in on and or what which how is are was were for from to with used use uses formulas formula equacoes equacao equations equation matematicas matematica mathematical fisicas fisica physical experimento experiment experimentos experiments estudo estudos study studies psi report relatorio'.split())
EXPANSIONS = {
    'calor': {'heat', 'thermal', 'fourier'}, 'termica': {'thermal', 'heat'},
    'conducao': {'conduction', 'fourier'}, 'radiacao': {'radiation', 'radiative'},
    'conservacao': {'conservation'}, 'massa': {'mass', 'continuity'},
    'fluidos': {'fluid', 'dynamics'}, 'escoamento': {'flow', 'fluid'},
    'combustao': {'combustion', 'flame'}, 'chama': {'flame'},
    'conveccao': {'convection', 'buoyancy', 'richardson'},
    'microgravidade': {'microgravity'}, 'difusao': {'diffusion'},
    'velocidade': {'velocity', 'speed'}, 'energia': {'energy'},
    'gota': {'droplet', 'drop'}, 'gotas': {'droplet', 'droplets', 'drop'}, 'queima': {'burning', 'burn'},
    'taxa': {'rate'}, 'propagacao': {'spread', 'spreading', 'propagation'}, 'ignicao': {'ignition'},
    'extincao': {'extinction', 'quenching'}, 'fuligem': {'soot'}, 'fumaca': {'smoke'},
    'particulas': {'particle', 'particles'}, 'pressao': {'pressure'}, 'oxigenio': {'oxygen'},
    'gravidade': {'gravity'}, 'empuxo': {'buoyancy', 'buoyant'}, 'diametro': {'diameter'},
    'tempo': {'time'}, 'vida': {'lifetime'}, 'temperatura': {'temperature'}, 'solido': {'solid'},
    'combustivel': {'fuel'}, 'radiativa': {'radiative', 'radiation'}, 'calibracao': {'calibration'},
}
# 'D²' normalizes to 'd2', shorter than the word filter keeps.
SQUARED_LAW = re.compile(r'\bd\s*(?:2|\^\s*2)\b')


def formula_question(query):
    text = normalized(query.replace('´', ''))
    # Also accept the omitted-l typo "formuas" (and a trailing keyboard d),
    # without routing ordinary words such as "formato" to mathematical retrieval.
    # Typed letter swaps seen in the chat ('fromulas', 'formluas') count too.
    return bool(re.search(r'\b(?:formulas?|formuasd?|fromulas?|formluas?|equacoes?|equacao|equations?|'
                          r'mathematical|matematic\w*)\b', text))


def words(text):
    return set(re.findall(r'[a-z][a-z0-9]{2,}', normalized(text))) - STOP


def experiment_match(experiment, group):
    return any(experiment == name or
               (name in {'ACME', 'SAME'} and experiment.startswith(name + ' '))
               for name in group)


def load_machine_items(path, documents):
    """Automatic transcriptions that belong to a PDF page of the active corpus revision."""
    path = Path(path) if path else None
    if not path or not path.is_file():
        return []
    pages = {(d['metadata']['document_id'], d['metadata'].get('content_sha256'), d['metadata'].get('page'))
             for d in documents}
    items = (json.loads(line) for line in path.read_text(encoding='utf-8').splitlines() if line.strip())
    return [item for item in items if (item['document_id'], item['content_sha256'], item['page']) in pages]


def build_formula_catalog(documents, reviewed=None, machine=()):
    if reviewed is None:
        reviewed = json.loads(Path(__file__).with_name('reviewed_formulas.json').read_text(encoding='utf-8'))
    annotations = {}
    for item in reviewed:
        key = (item['document_id'], item['content_sha256'], item['page'])
        annotations.setdefault(key, []).append(item)
    transcribed = {}
    for item in machine:
        if item['kind'] == 'formula':
            transcribed.setdefault((item['document_id'], item['content_sha256'], item['page']), []).append(item)
    pages = []
    seen = set()
    for doc in documents:
        meta, text = doc['metadata'], doc['content']
        if not str(meta.get('local_file', '')).lower().endswith('.pdf') or type(meta.get('page')) is not int:
            continue
        # A reference list cites equations' sources; it does not document their use.
        if meta.get('document_role') == 'bibliography':
            continue
        key = (meta['document_id'], meta.get('content_sha256'), meta['page'])
        if key in seen:
            continue
        seen.add(key)
        # URL query strings and '=' in a chemical formula can be false positives.
        # Keep the status explicitly as candidate and never count these as laws.
        lines = '\n'.join(line for line in text.splitlines() if 'http' not in line.lower())
        signals = len(re.findall(r'[=∑∫∂√∝]', lines))
        checked = [item for item in annotations.get(key, [])
                   if item.get('transcription_status') == 'visually_checked_against_pdf'
                   and normalized(item['anchor']) in normalized(text)]
        automatic = transcribed.get(key, [])
        if not signals and not checked and not automatic:
            continue
        # A full page keeps fractions and their adjacent definitions together.
        # Oversized pages stay in ordinary retrieval instead of being cut mid-equation.
        if len(text) > 18_000:
            continue
        labels = ' '.join(item['name'] + ' ' + ' '.join(item['topics']) for item in checked)
        meanings = ' '.join(s['meaning'] for item in automatic for s in item.get('symbols', []))
        pages.append({'metadata': meta, 'content': text, 'checked': checked, 'automatic': automatic,
                      'words': words(text + ' ' + labels + ' ' + meanings), 'signals': signals})
    return pages


def formula_context(catalog, query, psi_ids=(), experiment_groups=(), limit=3):
    if not formula_question(query):
        return []
    query_words = words(re.sub(r'\bPSI[-\s]?\d+\b', '', query, flags=re.I))
    for word in list(query_words):
        query_words.update(EXPANSIONS.get(word, ()))
    if SQUARED_LAW.search(normalized(query)):
        query_words.update({'droplet', 'burning', 'diameter', 'squared'})
    scoped = bool(psi_ids or experiment_groups)
    ranked = []
    for page in catalog:
        meta = page['metadata']
        ids = meta.get('psi_id', [])
        ids = [ids] if isinstance(ids, str) else ids
        if psi_ids:
            scope = [pid for pid in psi_ids if pid in ids]
        else:
            scope = [str(n) for n, group in enumerate(experiment_groups)
                     if experiment_match(meta.get('experiment') or '', group)]
        if scoped and not scope:
            continue
        overlap = len(query_words & page['words'])
        if not scoped and query_words and not overlap:
            continue
        # A formula whose own symbols match the question ('D = droplet diameter') says
        # more than a long page that merely shares vocabulary.
        symbol_overlap = max((len(query_words & words(' '.join(s['meaning'] for s in item.get('symbols', []))))
                              for item in page.get('automatic') or []), default=0)
        # Topic overlap comes first; checked transcriptions win over equally
        # relevant unreviewed math. This number is NOT a retrieval similarity.
        score = (overlap + 2 * symbol_overlap, symbol_overlap, bool(page['checked']), bool(page.get('automatic')),
                 min(page['signals'], 15))
        ranked.append((score, page, scope))
    ranked.sort(key=lambda entry: entry[0], reverse=True)
    # Reserve a page per explicit investigation/family before filling the limit.
    requested = list(psi_ids) if psi_ids else [str(n) for n in range(len(experiment_groups))]
    ordered = []
    for scope in requested:
        match = next((item for item in ranked if scope in item[2]), None)
        if match and match not in ordered:
            ordered.append(match)
    ordered += [item for item in ranked if item not in ordered]
    result, counts, seen_pages = [], Counter(), set()
    for _, page, scope in ordered:
        meta = page['metadata']
        document_id = meta['document_id']
        # Two copies of one report (e.g. with and without hyperlinks) are one page of
        # evidence; the same text requested for another investigation is kept.
        fingerprint = (normalized(page['content'])[:2000], tuple(scope))
        if counts[document_id] >= 2 or fingerprint in seen_pages:
            continue
        seen_pages.add(fingerprint)
        counts[document_id] += 1
        checked = page['checked']
        header = 'Mathematical context candidate. This is not a validated equation inventory.\n'
        if checked:
            header += 'Version-pinned transcriptions checked visually against this PDF page (not scientific validation):\n'
            for item in checked:
                header += f"{item['name']} — equation ({item['equation_number']}):\n$$\n{item['latex']}\n$$\n{item['context']}\n"
        automatic = page.get('automatic') or []
        if automatic:
            header += ('Automatic transcriptions of equation crops on this page (machine-made, unreviewed; '
                       'the PDF page is authoritative):\n')
            for item in automatic:
                number = f" ({item['number']})" if item.get('number') else ''
                doubt = ' [uncertain reading]' if not item.get('legible', True) else ''
                header += f"- equation{number}{doubt}:\n$$\n{item['latex']}\n$$\n"
                if item.get('symbols'):
                    header += '  symbols: ' + '; '.join(f"{s['symbol']} = {s['meaning']}" for s in item['symbols']) + '\n'
        header += '\nOriginal page text follows. Its mathematical layout may be incomplete:\n'
        result.append(SimpleNamespace(
            id=f"formula:{document_id}:{meta['page']}", score=None,
            payload={'page_content': header + page['content'], 'metadata': {
                **meta, 'retrieval_method': 'formula_page_catalog',
                'formula_validation': ('visual_transcription_available' if checked else
                                       'machine_transcription_unreviewed' if automatic else 'unreviewed_candidate'),
            }}))
        if len(result) >= limit:
            break
    return result


COMMON = re.compile(r'\b(?:comu(?:m|ns|mente)|frequente\w*|mais (?:usad|utilizad|recorrent|citad|aplicad)\w*|recorrente\w*|'
                    r'tipic\w*|common\w*|frequent\w*|most (?:used|common|cited|frequent)|recurring|typical\w*|'
                    r'usad\w* com frequencia|aparecem mais)\b')


def common_formula_request(query):
    return formula_question(query) and bool(COMMON.search(normalized(query)))


def frequency_sources(groups, experiment_groups=(), psi_ids=(), limit=8):
    """Grouped automatic transcriptions, most widespread first, as numbered evidence."""
    def scoped(group):
        examples = group['examples']
        if psi_ids:
            return [e for e in examples if set(e['psi_id'] if isinstance(e['psi_id'], list) else [e['psi_id']]) & set(psi_ids)]
        if experiment_groups:
            return [e for e in examples if any(experiment_match(e.get('experiment') or '', g) for g in experiment_groups)]
        return examples

    def pick(candidates, count):
        # A group whose every reading is uncertain is not offered as a complete formula.
        found = [(group, [e for e in scoped(group) if e.get('legible', True)]) for group in candidates]
        return [(group, examples) for group, examples in found if examples][:count]

    formula_groups = [g for g in groups if g['kind'] == 'formula']
    # Physical laws, models and fits first; at most two instrument signal conversions, labelled.
    conversions = pick([g for g in formula_groups if g.get('category') == 'instrument_conversion'], 2)
    chosen = pick([g for g in formula_groups if g.get('category') != 'instrument_conversion'],
                  limit - len(conversions)) + conversions
    coverage = {'formula_groups': len(formula_groups),
                'formula_occurrences': sum(g['occurrences'] for g in formula_groups),
                'groups_in_more_than_one_investigation': sum(1 for g in formula_groups if len(g['investigations']) > 1)}
    sources = []
    for group, examples in chosen:
        first = examples[0]
        where = '; '.join(f"{e.get('experiment') or '?'} — {e.get('title')}, p. {e['page']}" for e in examples[:6])
        label = ('Instrument calibration or signal/unit conversion, not a physical law'
                 if group.get('category') == 'instrument_conversion'
                 else 'Physical relation, model or fit')
        text = (f"Grouped automatic transcription (machine-made, unreviewed; check the cited page). {label}:\n$$\n{group['latex']}\n$$\n"
                f"Identical transcription found {group['occurrences']} time(s) in {group['documents']} document(s) "
                f"of {len(group['investigations'])} investigation(s) of this corpus: {', '.join(group['investigations'])}.\n"
                f"Pages: {where}.")
        meanings = {s['symbol']: s['meaning'] for e in examples for s in (e.get('symbols') or [])}
        if meanings:
            text += '\nSymbols defined on those pages: ' + '; '.join(f'{k} = {v}' for k, v in meanings.items())
        meta = {key: first.get(key) for key in ('document_id', 'title', 'experiment', 'psi_id', 'study_group',
                                                 'source', 'source_type', 'url', 'local_file', 'content_sha256', 'page')}
        meta.update(retrieval_method='formula_frequency', formula_validation='machine_transcription_unreviewed',
                    topic='microgravity_combustion', evidence_level='full_text', formula_group=group['group_id'],
                    data_warnings=['Automatic transcription from an image, not reviewed; the count applies only to this collection.'])
        sources.append(SimpleNamespace(id=f"formula-group:{group['group_id']}", score=None,
                                       payload={'page_content': text, 'metadata': meta}))
    return sources, coverage
