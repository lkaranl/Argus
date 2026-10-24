# Prompt de Criação: Argus (CCTV Command Hub em Go)

> **Instruções para o Agente:** Utilize este documento como a diretriz mestra e especificação de requisitos para implementar o **Argus** do zero. Siga rigorosamente as tecnologias, arquiteturas e restrições descritas abaixo. Nenhuma decisão de arquitetura deve desviar deste guia.

---

## 1. Visão Geral do Produto e Contexto Comercial

O **Argus** é uma central de monitoramento CFTV (CCTV Command Hub) comercial, ultra-leve e de altíssimo desempenho, voltada para exibição contínua 24/7 em TVs, monitores de portaria e quiosques.

### Objetivos Principais:
1. **Foco Comercial para Venda**: Produto empacotado como um **binário único compilado em Go**, de distribuição simples, sem dependências externas e com código-fonte protegido contra engenharia reversa.
2. **Hardware de Baixo Custo (Servidor e Cliente Fracos)**:
   - **Servidor**: Deve rodar com folga em máquinas modestas sem placa de vídeo (mini-PCs Celeron, computadores antigos de escritório, Raspberry Pi ou VPS básicas). Uso de CPU < 2% e RAM < 35 MB.
   - **Cliente (Navegador)**: 100% operado via browser (Chrome, Edge, Firefox, TV Browsers). Deve extrair **o FPS máximo nativo das câmeras (30 a 60 FPS cravados em 2K/1080p)** através de decodificação por aceleração de hardware nativa.
3. **Zero IA / Zero Transcodificação**: Não possui detecção de objetos, YOLO ou reconhecimento facial. O servidor faz apenas *passthrough* de pacotes de rede e agregação de telemetria.

---

## 2. Stack Tecnológica Obrigatória (Sem Ambiguidade)

### Backend (Servidor):
- **Linguagem**: **Go (Golang) 1.22+**
- **Roteamento & HTTP API**: `net/http` nativo com `go-chi/chi/v5` (leve, sem alocações desnecessárias).
- **Streaming de Vídeo**: `pion/webrtc/v4` e `AlexxIT/go2rtc` (utilizado como biblioteca/núcleo em Go ou wrapper integrado) para ingestão RTSP e saída WebRTC via protocolo **WHEP (WebRTC HTTP Egress Protocol)**.
- **Transcodificação de Vídeo**: **PROIBIDA NO SERVIDOR**. O streaming deve ser estritamente *Zero-Copy Passthrough* (RTP over RTSP ➔ RTP over WebRTC SRTP) com codec **H.264**.
- **Comunicação em Tempo Real**: **SSE (Server-Sent Events)** para envio leve de telemetria (Watts da usina solar e dados de clima a cada alteração).
- **Configuração**: Arquivo único `config.yaml` com suporte a substituição por variáveis de ambiente.
- **Distribuição**: Binário estático compilado com `//go:embed` para embutir todos os arquivos do frontend dentro do executável.

### Frontend (Cliente no Navegador):
- **Abordagem**: **100% Vanilla (HTML5 / CSS3 Puro / JavaScript ES6+ Puro)**.
- **Frameworks Proibidos no Cliente**: Proibido usar React, Vue, Angular ou qualquer Virtual DOM que dispute CPU com a decodificação de vídeo.
- **Renderização de Vídeo**: Elementos `<video>` conectados diretamente a conexões WebRTC via WHEP.
- **Aceleração por Hardware no CSS**: Uso de propriedades de composição direta de GPU (`will-change: transform`, `transform: translateZ(0)`, `isolation: isolate`) para garantir que atualizações do relógio ou dos dados solares não disparem *re-layout* ou redesenho dos vídeos das câmeras.

---

## 3. Módulos do Sistema e Funcionalidades

### Módulo 1: Gateway de Streaming WebRTC (WHEP)
- Conecta-se às câmeras IP configuradas via RTSP (sub-stream e main-stream).
- Expõe endpoints WHEP (`POST /api/whep/{camera_id}`) para negociar a sessão WebRTC diretamente com o navegador via SDP Offer/Answer.
- Mantém reconexão automática e silenciosa caso a câmera perca o sinal de rede.
- Latência esperada no navegador: **< 200ms (tempo real)**.

### Módulo 2: Usina Fotovoltaica (Huawei FusionSolar)
- Comunica-se com a API oficial em nuvem da Huawei (`https://intl.fusionsolar.huawei.com/thirdData/v1/...`).
- Realiza autenticação via token e renovação automática de sessão em background.
- Rotina de atualização (polling): a cada **5 minutos** (respeitando os limites de taxa da API da Huawei).
- Métricas coletadas:
  - Potência instantânea em tempo real (`kW`).
  - Geração acumulada do dia (`kWh`).
  - Status operacional da usina (Normal / Falha / Desconectada).
- Transmissão para a tela via SSE sem recarregar a página.

### Módulo 3: Clima e Previsão Meteorológica (Open-Meteo)
- Consulta a API gratuita e de alta precisão da Open-Meteo com coordenadas de latitude/longitude configuráveis no `config.yaml`.
- Rotina de atualização (polling): a cada **30 minutos** com cache em memória no servidor.
- Métricas coletadas:
  - Temperatura atual (`°C`), sensação térmica e velocidade do vento.
  - Condição climática atual (Céu limpo, Parcialmente nublado, Chuva, etc.).
  - Previsão dos próximos 3 dias com máximas, mínimas e ícones climáticos.

### Módulo 4: Relógio e Data Operacional de CFTV
- Widget de relógio digital de alta precisão (Horas, Minutos, Segundos e Data completa em pt-BR).
- Sincronização inicial com o servidor e contador contínuo local no navegador a cada 1000ms.

### Módulo 5: Layout e Interface (Command Center Grid 2x2)
- Layout em tela cheia otimizado para TVs de 32" a 65" e monitores 1080p/4K:
  - **Quadrante 1 (Superior Esquerdo)**: Câmera 1 (ex: Frente / Portão).
  - **Quadrante 2 (Superior Direito)**: Câmera 2 (ex: Garagem / Entrada).
  - **Quadrante 3 (Inferior Esquerdo)**: Câmera 3 (ex: Perímetro / Quintal).
  - **Quadrante 4 (Inferior Direito)**: Painel Informativo HUD Tático contendo:
    - Relógio Digital CFTV em fonte monoespaçada.
    - Widget da Usina Solar com indicador de geração atual e acumulado do dia.
    - Widget Climático com temperatura atual e previsão de 3 dias.
    - Badges de telemetria do sistema (Uptime, Status das Câmeras).
- Modo Tela Cheia automático com duplo clique ou atalho de teclado (`F11`).

---

## 4. Estrutura de Diretórios Recomendada para o Projeto

```text
argus/
├── cmd/
│   └── server/
│       └── main.go              # Ponto de entrada da aplicação
├── internal/
│   ├── config/
│   │   └── config.go            # Leitor de config.yaml e variáveis de ambiente
│   ├── stream/
│   │   ├── engine.go            # Gerenciador de streams Pion WebRTC / go2rtc
│   │   └── whep.go              # Handler do protocolo WHEP (SDP Answer)
│   ├── solar/
│   │   └── fusionsolar.go       # Cliente da API Huawei FusionSolar
│   ├── weather/
│   │   └── openmeteo.go         # Cliente da API Open-Meteo
│   └── sse/
│       └── broker.go            # Distribuidor de eventos em tempo real para o browser
├── web/
│   ├── embed.go                 # Diretiva //go:embed para empacotar o frontend
│   └── static/
│       ├── index.html           # Grid 2x2 CCTV Command Hub
│       ├── css/
│       │   └── style.css        # Estilo HUD tático escuro acelerado por GPU
│       └── js/
│           ├── webrtc.js        # Negociação WebRTC WHEP com auto-reconnect
│           ├── clock.js         # Relógio desacoplado de alta precisão
│           └── telemetria.js    # Consumidor de SSE para Solar e Clima
├── config.example.yaml          # Exemplo de configuração com câmeras e credenciais
├── go.mod
├── go.sum
└── README.md                    # Instruções de compilação e deploy
```

---

## 5. Exemplo de Arquivo de Configuração (`config.yaml`)

```yaml
server:
  port: 8080
  bind_address: "0.0.0.0"

cameras:
  - id: "cam_frente"
    name: "Câmera 01 — Frente"
    rtsp_url: "rtsp://admin:senha@192.168.1.101:554/stream1"
  - id: "cam_garagem"
    name: "Câmera 02 — Garagem"
    rtsp_url: "rtsp://admin:senha@192.168.1.102:554/stream1"
  - id: "cam_fundos"
    name: "Câmera 03 — Fundos"
    rtsp_url: "rtsp://admin:senha@192.168.1.103:554/stream1"

solar:
  enabled: true
  provider: "huawei_fusionsolar"
  api_url: "https://intl.fusionsolar.huawei.com"
  username: "SEU_USUARIO"
  system_code: "SUA_SENHA_SISTEMA"
  plant_code: "CODIGO_DA_PLANTA"
  poll_interval_minutes: 5

weather:
  enabled: true
  latitude: -10.8778   # Exemplo: Ji-Paraná, RO
  longitude: -61.9514
  city_name: "Ji-Paraná, RO"
  poll_interval_minutes: 30
```

---

## 6. Diretrizes de Compilação e Entrega Comercial

Para gerar o binário único comercial que será vendido e instalado no cliente:
- O binário deve ser estático e otimizado com remoção de símbolos de debug (`-ldflags="-s -w"`):
  ```bash
  CGO_ENABLED=0 go build -ldflags="-s -w" -o argus cmd/server/main.go
  ```
- O arquivo resultante terá aproximadamente **15MB a 25MB**, contendo tudo o que é necessário para rodar como um serviço do sistema (`systemd` no Linux ou serviço do Windows).

---

## 7. Critérios de Aceite e Testes de Performance
1. **Consumo de Memória no Servidor**: Em repouso com 3 ou 4 streams ativos, o consumo de RAM do processo Go não pode exceder **35 MB**.
2. **Uso de CPU no Servidor**: Menor que **2%** em CPUs x86_64 padrão (sem GPU).
3. **Fluidez no Cliente**: 30 FPS cravados em cada câmera na tela sem congelamento ou atraso acumulado (*delay drift*).
4. **Resiliência de Rede**: Ao puxar o cabo de rede de uma câmera e reconectar, o stream na tela deve se recuperar sozinho sem exigir recarga da página (`F5`).
