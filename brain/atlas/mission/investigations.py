"""Ranking explicável de relevância, sem inferir probabilidade ou equivalência entre combustíveis."""
import json
import math
from pathlib import Path

CATALOG = [
    dict(id='BASS-II', family='sólidos', pillar='Prevenção', status='Ensaios em microgravidade',
         objective='Investigar queima e extinção de sólidos sob diferentes escoamentos.',
         samples='Tecidos e plásticos; formatos e dimensões variam por ensaio.',
         hardware='Duto com ventilador · MSG', measurements='Imagens, propagação e extinção',
         variables='Material, O₂ e fluxo de ar', limitation='Pressão de cabine assumida; amostras não representam qualquer material da nave.'),
    dict(id='Saffire-II', family='sólidos', pillar='Prevenção', status='Voo + comparação terrestre',
         objective='Comparar inflamabilidade de materiais em escala de espaçonave.',
         samples='Silicone, tecidos e polímeros; dimensões registradas por linha.',
         hardware='Módulo experimental no Cygnus', measurements='Câmeras, comprimento queimado e propagação',
         variables='Material, espessura, O₂ e escoamento', limitation='Colunas de 1 g têm resultados, mas não todas as condições do ensaio terrestre.'),
    dict(id='ACME CFI-G', family='gases', pillar='Prevenção', status='Outra família de combustível',
         objective='Investigar chamas frias gasosas, normais e inversas.',
         samples='Combustíveis gasosos; queimador esférico.', hardware='ACME · CIR',
         measurements='Ignição, duração, extinção e temperatura do queimador',
         variables='Composição, pressão e vazão; nas chamas inversas o O₂ sai do queimador.',
         limitation='Até 85% de O₂ no oxidante do queimador não equivale a uma cabine a 85%; não valida sólidos.'),
    dict(id='ACME E-FIELD Flames', family='gases', pillar='Prevenção', status='Analogia por campo elétrico',
         objective='Estudar como campos elétricos alteram chamas por ação nos íons.',
         samples='Chamas de difusão de combustível gasoso.', hardware='ACME · CIR',
         measurements='Forma e comportamento da chama', variables='Campo elétrico e escoamento',
         limitation='Gravidade imitada por outra força; não é medição direta na Lua nem validação de tecidos.'),
    dict(id='LUCI', family='sólidos', pillar='Prevenção', status='Resultados documentais · NTRS 2025',
         objective='Investigar inflamabilidade em gravidade lunar simulada por rotação.',
         samples='Tecido algodão/fibra de vidro e haste de PMMA de 4 mm.', hardware='Experimento em foguete suborbital rotativo',
         measurements='Duas câmeras e concentração de O₂', variables='Combustível, orientação e queda de O₂ durante a queima',
         limitation='Há resultados no NTRS; não há ensaios LUCI normalizados nesta base PSI. Efeitos de rotação exigem cautela.'),
    dict(id='FM2', family='sólidos', pillar='Prevenção', status='Planejado na fonte · NTRS 2024',
         objective='Investigar materiais na superfície lunar e em atmosferas de exploração.',
         samples='Tecidos SIBAL e hastes acrílicas previstos.', hardware='Câmara autônoma planejada para a Lua',
         measurements='Propagação, extinção e atmosfera previstas', variables='21%/14,7 psi e 34%/8,2 psi; orientação',
         limitation='Protótipo e planejamento na fonte disponível; não é resultado lunar medido nesta base.'),
    dict(id='SAME', family='aerossóis', pillar='Detecção', status='Experimento em microgravidade',
         objective='Caracterizar partículas de fumaça relevantes à detecção.',
         samples='Materiais de cabine aquecidos para produzir fumaça.', hardware='MSG',
         measurements='Distribuição de tamanho de partículas e detectores', variables='Material, aquecimento, fluxo e envelhecimento da fumaça',
         limitation='Transporte até o sensor depende da geometria; os dados não fornecem o tempo de alarme desta cabine.'),
    dict(id='SAME-R', family='aerossóis', pillar='Detecção', status='Experimento em microgravidade',
         objective='Avaliar partículas e desempenho da detecção de fumaça.',
         samples='Fumaça de materiais de cabine aquecidos.', hardware='MSG', measurements='Instrumentos de aerossóis e detectores de fumaça',
         variables='Material e velocidade de entrada', limitation='Resposta depende do aerossol e do sensor; baixo fluxo não é garantia de extinção.'),
    dict(id='FLEX', family='gotas líquidas', pillar='Supressão', status='Outra família de combustível',
         objective='Estudar extinção de gotas com diluentes como CO₂ e hélio.',
         samples='Gotas de combustíveis líquidos, incluindo metanol e heptano.', hardware='MDCA · CIR',
         measurements='Imagens de gotas e comportamento de extinção', variables='Combustível, O₂, diluente e pressão',
         limitation='Não determina dose de supressor segura para uma cabine tripulada nem para fogo em sólidos.'),
]


def detection(flow: float | None, gravity: float) -> dict:
    micro = gravity < 0.01
    status = 'Fluxo não informado' if flow is None else 'Sem transporte forçado' if flow == 0 else 'Transporte forçado presente'
    return {'status': status, 'flow_cm_s': flow,
            'seconds_per_metre': round(100 / flow, 2) if flow is not None and flow > 0 else None,
            'equation': 't = L / u; para 1 m, t = 100 / u(cm/s)',
            'interpretation': ('Em microgravidade, não há subida de fumaça por empuxo como na Terra; o escoamento conduz partículas ao detector.'
                               if micro else 'Na gravidade parcial, empuxo e ventilação podem coexistir; o caminho até o sensor precisa ser estudado.'),
            'limitation': 'Tempo de advecção ideal por metro, não tempo de alarme. Faltam caminho real, mistura, aerossol e resposta do sensor. Fluxo zero deixa este cálculo indefinido.',
            'tradeoff': 'A ventilação resfria equipamentos e transporta fumaça, mas também pode alimentar a chama. Cortá-la exige avaliar os três efeitos.',
            'sources': ['https://psi.nasa.gov/physci/repo/data/investigations/PSI-102',
                        'https://psi.nasa.gov/physci/repo/data/investigations/PSI-101']}


class Investigations:
    def __init__(self, data: Path, pages: list[dict], solids: list[dict]):
        self.data = json.loads((data / 'challenge_evidence.json').read_text(encoding='utf-8'))
        self.pages, self.solids = pages, solids

    def detail(self, state) -> dict:
        gas = self.data['cfi_g_tests']
        distance = lambda t: abs(t['oxygen_pct'] - state.oxygen_pct) / 2 + abs(math.log(t['pressure_kpa'] / state.pressure_kpa)) / .2
        selected = []
        for kind in ('normal', 'inversa'):
            selected += sorted((t for t in gas if t['configuration'] == kind), key=distance)[:2]
        ranked = []
        for item in CATALOG:
            score, reasons = 0, []
            def add(points, text):
                nonlocal score
                score += points
                reasons.append({'points': points, 'text': text})
            if item['family'] == 'sólidos':
                add(30, 'Família diretamente ligada aos materiais do habitat')
            if item['pillar'] == 'Detecção':
                add(25, 'Detecção é uma necessidade do ambiente tripulado')
                if state.flow_cm_s is not None and state.flow_cm_s < 5:
                    add(55, 'Fluxo abaixo de 5 cm/s: priorizar transporte de fumaça (regra de ranking, não limiar de alarme)')
            if item['id'] in ('BASS-II', 'Saffire-II'):
                tests = [t for t in self.solids if t['experiment'] == item['id']]
                if any(abs(t['oxygen_pct'] - state.oxygen_pct) <= 2 and abs(math.log(t['pressure_kpa'] / state.pressure_kpa)) <= .2 for t in tests):
                    add(25, 'Há linhas próximas em O₂ e pressão assumida')
                if state.gravity_g < .01:
                    add(20, 'Microgravidade do cenário coincide com o regime dos ensaios')
                elif item['id'] == 'Saffire-II':
                    add(12, 'Resultados de voo e de 1 g permitem comparação qualitativa')
            if item['id'] == 'ACME CFI-G':
                add(15, 'Ensaios gasosos com composição e pressão tabeladas')
                if state.oxygen_pct >= 28:
                    add(30, 'O₂ enriquecido torna relevante ler os ensaios de alto O₂, distinguindo queimador e câmara')
                if state.oxygen_pct > 85:
                    add(0, 'O₂ do cenário excede até o máximo de 85% do oxidante nos ensaios inversos')
            if 0.01 <= state.gravity_g < .95:
                if item['id'] == 'LUCI': add(30, 'Resultados documentais em gravidade lunar simulada')
                if item['id'] == 'FM2': add(12, 'Planejamento voltado à lacuna de dados lunares')
                if item['id'] == 'ACME E-FIELD Flames': add(20, 'Analogia de forças ajuda a investigar gravidade parcial')
            if item['id'] == 'FLEX': add(25, 'Fundamentos de supressão por diluição')
            if not reasons: add(5, 'Contexto científico complementar')
            docs = [p for p in self.pages if p['experiment'] == item['id']]
            sources = []
            for p in docs:
                if p['url'] and not any(s['url'] == p['url'] for s in sources):
                    sources.append({k: p[k] for k in ('title', 'url', 'page')})
            # As fontes das tabelas existem mesmo quando nenhuma página documental foi extraída.
            if not sources:
                sources = [{k: t[k] for k in ('title', 'url')} for t in self.solids if t['experiment'] == item['id']][:1]
            ranked.append({**item, 'score': score, 'reasons': reasons, 'sources': sources[:2]})
        ranked.sort(key=lambda x: (-x['score'], x['id']))
        return {'investigations': ranked,
                'ranking_rule': 'Pontuação heurística de relevância: soma dos motivos exibidos. Não mede risco, confiança ou probabilidade. Não há interpolação entre famílias.',
                'cfi_g': {'count': len(gas), 'nearest': selected,
                          'inverse_oxygen_range': [min(t['oxygen_pct'] for t in gas if t['configuration']=='inversa'), max(t['oxygen_pct'] for t in gas if t['configuration']=='inversa')],
                          'note': 'Normais: O₂ inicial na câmara. Inversas: O₂ no oxidante do queimador. Proximidade numérica não é equivalência com sólidos ou com a atmosfera do habitat.'},
                'saffire_ground': self.data['saffire_ground'], 'detection': detection(state.flow_cm_s, state.gravity_g)}
