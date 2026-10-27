# Especificação de Design: Redesign do Painel de Energia Solar (Tesla Solar Command)

## 1. Visão Geral
Redesenho completo do componente de telemetria de energia solar no HUD tático do Argus (`web/static/js/telemetry.js` e `web/static/css/style.css`), eliminando o gráfico de barras semanal que ficava com colunas vazias e substituindo o layout fragmentado por um design integrado de alta densidade informativa e fluidez visual inspirado no painel Tesla Solar.

## 2. Diagnóstico dos Problemas Anteriores
1. **Espaço Vazio Excessivo**: O gráfico de barras dos 7 dias consumia ~60px de altura, mas continha 6 colunas vazias (`·`) porque o banco de histórico estava recém-inicializado, gerando um vácuo escuro no meio do card.
2. **Espremimento e Distorção do Gráfico**: A curva diária foi colocada ao lado do número em uma caixa de 48px de altura com `preserveAspectRatio="none"`, achatando e distorcendo a tipografia do SVG.
3. **Duplicação de Métricas**: O valor de hoje aparecia na barra `HOJE` e no pill `HOJE KWH`.

## 3. Arquitetura do Novo Layout (Tesla Solar Command)

### 3.1 Cabeçalho
- **Esquerda**: `☀️ MINHA ENERGIA SOLAR` com dot pulsante verde `AO VIVO`.
- **Direita**: Badge `PICO HOJE: X.X kW` com tipografia mono âmbar.

### 3.2 Faixa Superior de KPIs (Grid 4 Colunas)
- **AGORA**: Potência instantânea em kW em fonte monumental dourada (`#F5C542`).
- **HOJE**: Geração acumulada do dia em kWh.
- **MÊS**: Geração acumulada do mês em kWh/MWh.
- **TOTAL**: Eletricidade acumulada histórica em MWh.

### 3.3 Centro e Base: Curva Solar do Dia Expansiva (06:00 às 18:30)
- **Altura Expandida**: Ocupa todo o espaço vertical disponível (~90px de altura).
- **SVG com Aspect Ratio Preservado**: Curva suave vetorial (amostrada a cada 5 min da FusionSolar).
- **Gradiente Dourado Solar**: Preenchimento vertical translúcido sob a curva.
- **Ponto Solar ao Vivo**: Marcador pulsante (`#FFF275`) no ponto horário e kW do momento.
- **Linha de Horizonte Pontilhada**: Projeção suave até 18:30.
- **Timeline Nativa em HTML**: Marcações limpas no rodapé (`06:00`, `09:00`, `12:00`, `15:00`, `18:30`).

## 4. Impacto em Arquivos
- `web/static/js/telemetry.js`: Reformulação do método de renderização da curva diária e dos KPIs.
- `web/static/css/style.css`: Estilização dos novos containers flex/grid e remoção dos estilos obsoletos das barras semanais.
