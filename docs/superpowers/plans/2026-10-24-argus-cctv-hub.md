# Plano de Implementação — Argus (CCTV Command Hub em Go)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Construir o Argus do zero como um binário comercial único e estático em Go 1.22+, sem transcodificação, com motor WHEP WebRTC passthrough H.264 para CFTV, telemetria em tempo real (Huawei FusionSolar Kiosk e Open-Meteo) via SSE e frontend HUD tático 100% Vanilla embutido.

**Architecture:** Monólito Go de alta performance com roteador `go-chi/chi/v5`, motor de streaming WHEP zero-copy, hub de telemetria pub/sub via Server-Sent Events, workers periódicos resilientes e frontend Vanilla acelerado por GPU com relógio modular de 60 marcadores SVG e card solar estilo Tesla.

**Tech Stack:** Go 1.22+, `go-chi/chi/v5`, `pion/webrtc/v4`, `gopkg.in/yaml.v3`, `joho/godotenv`, HTML5 / CSS3 GPU-accelerated / JavaScript ES6+ Vanilla, `//go:embed`.

## Global Constraints

- Linguagem do Backend: Go 1.22+
- Uso de Memória no Servidor: < 35 MB de RAM
- Uso de CPU no Servidor: < 2% em repouso
- Zero transcodificação: estritamente H.264 passthrough
- Segredos e dados sensíveis via `.env` ou variáveis de ambiente do sistema
- Frontend 100% Vanilla sem frameworks (React, Vue, etc. são proibidos)
- Binário final único estático com `CGO_ENABLED=0` e `-ldflags="-s -w"`
- Mensagens e commits em português do Brasil

---

### Task 1: Inicialização do Módulo Go e Configuração com `.env` e `config.yaml`

**Files:**
- Create: `go.mod`
- Create: `.env.example`
- Create: `config.example.yaml`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: Arquivo `.env`, variáveis de ambiente e `config.yaml`.
- Produces: `config.Config`, `config.LoadConfig(configPath string) (*Config, error)`.

- [ ] **Step 1: Criar o teste unitário de configuração**
  Escrever `internal/config/config_test.go` testando o carregamento de parâmetros do servidor, câmeras com interpolação de variáveis de ambiente, configurações solares e climáticas.

- [ ] **Step 2: Executar o teste para verificar que falha**
  Executar: `go test ./internal/config/...`
  Esperado: FAIL (módulo ou pacote não encontrado).

- [ ] **Step 3: Inicializar o módulo Go e dependências básicas**
  Executar: `go mod init argus` e adicionar `gopkg.in/yaml.v3`, `joho/godotenv`, `go-chi/chi/v5`.

- [ ] **Step 4: Implementar `internal/config/config.go` e templates de configuração**
  Criar `.env.example`, `config.example.yaml` e implementar `internal/config/config.go` com substituição de `${VAR}` via `os.ExpandEnv` e leitura de `.env`.

- [ ] **Step 5: Executar o teste unitário de configuração e verificar aprovação**
  Executar: `go test -v ./internal/config/...`
  Esperado: PASS.

- [ ] **Step 6: Commit**
  ```bash
  git add go.mod go.sum .env.example config.example.yaml internal/config/
  git commit -m "feat(config): implementa carregador de configuração com suporte a .env e yaml"
  ```

---

### Task 2: Hub de Telemetria SSE (Server-Sent Events)

**Files:**
- Create: `internal/sse/broker.go`
- Test: `internal/sse/broker_test.go`

**Interfaces:**
- Consumes: Mensagens de telemetria em formato byte slice ou struct serializada.
- Produces: `sse.Broker`, `NewBroker() *Broker`, `broker.Broadcast(event string, data []byte)`, `broker.ServeHTTP(w http.ResponseWriter, r *http.Request)`.

- [ ] **Step 1: Criar o teste unitário do broker SSE**
  Escrever `internal/sse/broker_test.go` testando subscrição de cliente, broadcast de evento, buffer não-bloqueante e cancelamento de conexão sem travamentos.

- [ ] **Step 2: Executar o teste para verificar que falha**
  Executar: `go test ./internal/sse/...`
  Esperado: FAIL.

- [ ] **Step 3: Implementar `internal/sse/broker.go`**
  Implementar struct thread-safe com `sync.RWMutex`, registro de canais clientes, broadcast não-bloqueante e handler `ServeHTTP` compatível com `text/event-stream`.

- [ ] **Step 4: Executar o teste para verificar aprovação**
  Executar: `go test -v ./internal/sse/...`
  Esperado: PASS.

- [ ] **Step 5: Commit**
  ```bash
  git add internal/sse/
  git commit -m "feat(sse): implementa broker pub/sub thread-safe para Server-Sent Events"
  ```

---

### Task 3: Cliente HTTP Huawei FusionSolar (Kiosk REST API)

**Files:**
- Create: `internal/solar/fusionsolar.go`
- Test: `internal/solar/fusionsolar_test.go`

**Interfaces:**
- Consumes: `FUSIONSOLAR_TOKEN` ou `FUSIONSOLAR_KIOSK_URL`.
- Produces: `solar.Client`, `solar.SolarData`, `client.FetchData(ctx context.Context) (*SolarData, error)`.

- [ ] **Step 1: Criar o teste unitário do cliente FusionSolar**
  Escrever `internal/solar/fusionsolar_test.go` simulando um servidor HTTP local respondendo com o payload HTML-escaped da Huawei e validando extração de kW, kWh e fallback em caso de erro.

- [ ] **Step 2: Executar o teste para verificar que falha**
  Executar: `go test ./internal/solar/...`
  Esperado: FAIL.

- [ ] **Step 3: Implementar `internal/solar/fusionsolar.go`**
  Implementar extração de token `kk`, requisição ao endpoint `/rest/pvms/web/kiosk/v1/station-kiosk-file`, `html.UnescapeString`, deserialização de JSON e leitura de fallback de snapshot local.

- [ ] **Step 4: Executar o teste unitário e verificar aprovação**
  Executar: `go test -v ./internal/solar/...`
  Esperado: PASS.

- [ ] **Step 5: Commit**
  ```bash
  git add internal/solar/
  git commit -m "feat(solar): implementa cliente Kiosk REST da Huawei FusionSolar com unescape HTML"
  ```

---

### Task 4: Cliente HTTP Open-Meteo com Estimativa de Irradiação Solar

**Files:**
- Create: `internal/weather/openmeteo.go`
- Test: `internal/weather/openmeteo_test.go`

**Interfaces:**
- Consumes: Latitude, Longitude, Nome da cidade.
- Produces: `weather.Client`, `weather.WeatherData`, `client.FetchWeather(ctx context.Context) (*WeatherData, error)`.

- [ ] **Step 1: Criar o teste unitário do cliente Open-Meteo**
  Escrever `internal/weather/openmeteo_test.go` validando o cálculo de radiação $\text{MJ} / 3.6 = \text{kWh/m²}$, classificação de potencial solar (Excelente/Bom/Moderado/Baixo) e tradução de códigos WMO para pt-BR.

- [ ] **Step 2: Executar o teste para verificar que falha**
  Executar: `go test ./internal/weather/...`
  Esperado: FAIL.

- [ ] **Step 3: Implementar `internal/weather/openmeteo.go`**
  Implementar consulta à API Open-Meteo, conversão física de radiação solar, estimativa de geração teórica, mapeamento WMO e cache em memória.

- [ ] **Step 4: Executar o teste unitário e verificar aprovação**
  Executar: `go test -v ./internal/weather/...`
  Esperado: PASS.

- [ ] **Step 5: Commit**
  ```bash
  git add internal/weather/
  git commit -m "feat(weather): implementa cliente Open-Meteo com estimativa de irradiação solar e previsão"
  ```

---

### Task 5: Gerenciador de Telemetria e Workers em Background

**Files:**
- Create: `internal/telemetry/manager.go`
- Test: `internal/telemetry/manager_test.go`

**Interfaces:**
- Consumes: `*config.Config`, `*solar.Client`, `*weather.Client`, `*sse.Broker`.
- Produces: `telemetry.Manager`, `NewManager(...)`, `manager.Start(ctx context.Context)`, `manager.GetLatest() *CombinedTelemetry`.

- [ ] **Step 1: Criar o teste unitário do orquestrador de telemetria**
  Escrever `internal/telemetry/manager_test.go` testando agregação de dados solares, meteorológicos e uptime, garantindo despacho via broker SSE.

- [ ] **Step 2: Executar o teste para verificar que falha**
  Executar: `go test ./internal/telemetry/...`
  Esperado: FAIL.

- [ ] **Step 3: Implementar `internal/telemetry/manager.go`**
  Implementar loop em background com tickers configuráveis (5m solar, 30m clima) e cache thread-safe dos dados consolidados.

- [ ] **Step 4: Executar o teste unitário e verificar aprovação**
  Executar: `go test -v ./internal/telemetry/...`
  Esperado: PASS.

- [ ] **Step 5: Commit**
  ```bash
  git add internal/telemetry/
  git commit -m "feat(telemetry): orquestra coleta em background de solar e clima com despacho SSE"
  ```

---

### Task 6: Motor de Streaming RTSP e WHEP WebRTC

**Files:**
- Create: `internal/stream/engine.go`
- Create: `internal/stream/whep.go`
- Test: `internal/stream/whep_test.go`

**Interfaces:**
- Consumes: Lista de câmeras (`id`, `rtsp_url`), SDP Offer HTTP POST do navegador.
- Produces: `stream.Engine`, `NewEngine(cameras []config.Camera) *Engine`, `engine.HandleWHEP(w http.ResponseWriter, r *http.Request)`.

- [ ] **Step 1: Criar o teste unitário do handler WHEP**
  Escrever `internal/stream/whep_test.go` validando requisição inválida, câmera não encontrada, e rota WHEP processando requisições com SDP Offer.

- [ ] **Step 2: Executar o teste para verificar que falha**
  Executar: `go test ./internal/stream/...`
  Esperado: FAIL.

- [ ] **Step 3: Implementar `internal/stream/engine.go` e `internal/stream/whep.go`**
  Implementar engine de streaming com gerenciamento de conexões WebRTC via `pion/webrtc/v4`, roteamento de tracks H.264 e endpoint WHEP compatível com RFC/draft WHEP.

- [ ] **Step 4: Executar o teste unitário e verificar aprovação**
  Executar: `go test -v ./internal/stream/...`
  Esperado: PASS.

- [ ] **Step 5: Commit**
  ```bash
  git add internal/stream/
  git commit -m "feat(stream): implementa motor de streaming WebRTC e handler WHEP"
  ```

---

### Task 7: Frontend HUD Tático (HTML, CSS, JS com SVG Apple Clock & Tesla Solar Card)

**Files:**
- Create: `web/embed.go`
- Create: `web/static/index.html`
- Create: `web/static/css/style.css`
- Create: `web/static/js/clock.js`
- Create: `web/static/js/telemetry.js`
- Create: `web/static/js/webrtc.js`
- Test: `web/embed_test.go`

**Interfaces:**
- Consumes: Arquivos estáticos em `web/static/`.
- Produces: `web.StaticFS` via `//go:embed static/*`.

- [ ] **Step 1: Criar o teste de integridade do fs.FS embutido**
  Escrever `web/embed_test.go` verificando que os arquivos `index.html`, `css/style.css`, `js/clock.js`, `js/telemetry.js` e `js/webrtc.js` estão embutidos e não-vazios.

- [ ] **Step 2: Implementar arquivos estáticos do frontend**
  - `web/static/index.html`: Shell da central com grade adaptável de CFTV e contêiner do HUD Tático.
  - `web/static/css/style.css`: Estilização HUD dark tático com aceleração por hardware, bezel SVG, cores esmeralda e layout responsivo.
  - `web/static/js/clock.js`: AppleModularClock de alta precisão com 60 marcadores de segundos em SVG.
  - `web/static/js/telemetry.js`: Consumidor SSE com atualização cirúrgica via `textContent` e card solar Tesla.
  - `web/static/js/webrtc.js`: Player WHEP WebRTC com auto-reconnect exponencial.
  - `web/embed.go`: Declaração do `//go:embed static/*`.

- [ ] **Step 3: Executar o teste de embed e verificar aprovação**
  Executar: `go test -v ./web/...`
  Esperado: PASS.

- [ ] **Step 4: Commit**
  ```bash
  git add web/
  git commit -m "feat(ui): implementa frontend HUD tático vanilla com relógio SVG e card solar Tesla"
  ```

---

### Task 8: Servidor HTTP Principal e Integração Geral (`cmd/server/main.go`)

**Files:**
- Create: `cmd/server/main.go`
- Test: `cmd/server/server_test.go`

**Interfaces:**
- Consumes: Todos os módulos internos e `web.StaticFS`.
- Produces: Executável principal do Argus com graceful shutdown.

- [ ] **Step 1: Criar teste de integração do servidor HTTP**
  Escrever `cmd/server/server_test.go` iniciando o roteador Chi com os serviços e validando rotas `/api/status`, `/api/events`, `/api/cameras` e rota estática `/`.

- [ ] **Step 2: Executar teste para verificar que falha**
  Executar: `go test ./cmd/server/...`
  Esperado: FAIL.

- [ ] **Step 3: Implementar `cmd/server/main.go`**
  Implementar carregamento de configuração, inicialização do broker SSE, telemetry manager, engine de streaming, roteador Chi com middlewares leves (logger, recoverer, cors), graceful shutdown em `SIGINT`/`SIGTERM`.

- [ ] **Step 4: Executar o teste de integração e verificar aprovação**
  Executar: `go test -v ./cmd/server/...`
  Esperado: PASS.

- [ ] **Step 5: Commit**
  ```bash
  git add cmd/server/
  git commit -m "feat(server): integra servidor HTTP chi, graceful shutdown e rotas da API"
  ```

---

### Task 9: Validação Final, Build Estático Comercial e Documentação

**Files:**
- Create: `README.md` (atualizado)
- Create: `.gitignore`
- Test: Execução completa de testes unitários e `go vet`

- [ ] **Step 1: Criar `.gitignore`**
  Garantir que `.env`, binários compilados (`argus`), dados locais e caches não sejam commitados.

- [ ] **Step 2: Executar suíte completa de testes e análise estática**
  Executar: `go test -v ./...` e `go vet ./...`.
  Esperado: Todos os testes passando sem erros de linter.

- [ ] **Step 3: Compilar o binário comercial estático**
  Executar: `CGO_ENABLED=0 go build -ldflags="-s -w" -o argus cmd/server/main.go`.
  Verificar tamanho do binário e ausência de símbolos de debug.

- [ ] **Step 4: Atualizar README.md com documentação completa**
  Documentar arquitetura, configuração via `.env`, comandos de compilação e execução como serviço de sistema.

- [ ] **Step 5: Commit final**
  ```bash
  git add .gitignore README.md
  git commit -m "docs: finaliza documentação do Argus e instruções de distribuição comercial"
  ```
