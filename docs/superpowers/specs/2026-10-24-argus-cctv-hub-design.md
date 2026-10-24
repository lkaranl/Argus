# Especificação de Design — Argus (CCTV Command Hub em Go)

**Data:** 24 de Outubro de 2026  
**Status:** Aprovado  
**Autor:** Antigravity & Karan  

---

## 1. Visão Geral do Produto e Objetivos

O **Argus** é uma central de monitoramento CFTV comercial (CCTV Command Hub) ultra-leve e de altíssimo desempenho, voltada para exibição ininterrupta 24/7 em TVs, monitores de portaria e quiosques.

### Objetivos Comerciais e Técnicos:
1. **Binário Único Estático em Go**: Distribuição comercial em arquivo único (`CGO_ENABLED=0`), sem dependências externas instaladas no host, sem banco de dados local e com frontend embutido via `//go:embed`.
2. **Execução em Hardware de Baixo Custo**:
   - **Servidor**: Consumo de CPU < 2% e RAM < 35 MB em repouso com até 4 streams H.264 ativos, rodando com folga em mini-PCs Celeron, Raspberry Pi ou computadores corporativos modestos sem placa de vídeo.
   - **Cliente (Navegador)**: Desempenho nativo de 30 a 60 FPS cravados em 1080p/2K através de aceleração por hardware (VPU/GPU) nos navegadores modernos de TV e desktop.
3. **Zero Transcodificação no Servidor**: Roteamento estrito de pacotes de rede *Zero-Copy Passthrough* (RTP over RTSP ➔ RTP over WebRTC SRTP) em codec H.264.
4. **Telemetria Integrada em Tempo Real**: Envio de eventos leves via Server-Sent Events (SSE) para dados da Usina Solar Huawei e previsão climática Open-Meteo.

---

## 2. Gestão de Segredos e Configurações

Para garantir conformidade com boas práticas de segurança, **qualquer dado sensível ou credencial é injetado via `.env`** ou variáveis de ambiente do sistema operacional, mantendo o arquivo `config.yaml` puramente declarativo de topologia e parâmetros não-sensíveis.

### 2.1. Arquivo `.env` (Ignorado no Git)
```bash
# Servidor
ARGUS_PORT=8080
ARGUS_BIND_ADDRESS=0.0.0.0

# Usina Solar (Huawei FusionSolar Kiosk)
FUSIONSOLAR_ENABLED=true
# Pode ser informado o token de 32 caracteres ou a URL completa do quiosque
FUSIONSOLAR_TOKEN=EM6wrnyAhBtOFvD7rdNu045xGpwb1bGD
FUSIONSOLAR_KIOSK_URL=https://intl.fusionsolar.huawei.com/pvmswebsite/nologin/assets/build/cloud.html#/kiosk?kk=EM6wrnyAhBtOFvD7rdNu045xGpwb1bGD

# Câmeras RTSP (URLs com usuário e senha não commitadas)
CAM_1_RTSP=rtsp://admin:senha123@192.168.1.101:554/stream1
CAM_2_RTSP=rtsp://admin:senha123@192.168.1.102:554/stream1
CAM_3_RTSP=rtsp://admin:senha123@192.168.1.103:554/stream1
```

### 2.2. Arquivo `config.yaml`
```yaml
server:
  port: 8080
  bind_address: "0.0.0.0"

cameras:
  - id: "cam_frente"
    name: "Câmera 01 — Frente"
    rtsp_url: "${CAM_1_RTSP}"
  - id: "cam_garagem"
    name: "Câmera 02 — Garagem"
    rtsp_url: "${CAM_2_RTSP}"
  - id: "cam_fundos"
    name: "Câmera 03 — Fundos"
    rtsp_url: "${CAM_3_RTSP}"

solar:
  poll_interval_minutes: 5

weather:
  enabled: true
  latitude: -10.8778
  longitude: -61.9514
  city_name: "Ji-Paraná, RO"
  poll_interval_minutes: 30
```

---

## 3. Arquitetura de Software e Estrutura de Pacotes Go

```text
argus/
├── cmd/
│   └── server/
│       └── main.go                 # Inicialização, injeção de dependências e graceful shutdown
├── internal/
│   ├── config/
│   │   └── config.go               # Leitura de .env, config.yaml e variáveis de ambiente
│   ├── stream/
│   │   ├── engine.go               # Motor de streaming integrado com go2rtc core
│   │   └── whep.go                 # Handler HTTP do protocolo WHEP (SDP Offer/Answer)
│   ├── sse/
│   │   └── broker.go               # Hub pub/sub thread-safe de Server-Sent Events
│   ├── solar/
│   │   └── fusionsolar.go          # Cliente HTTP da API REST Kiosk da Huawei FusionSolar
│   ├── weather/
│   │   └── openmeteo.go            # Cliente HTTP Open-Meteo com estimativa solar
│   └── telemetry/
│       └── manager.go              # Coletor periódico que formata e despacha eventos no SSE
├── web/
│   ├── embed.go                    # Diretiva //go:embed static/*
│   └── static/
│       ├── index.html              # Grade responsiva de CFTV e contêineres de vídeo
│       ├── css/
│       │   └── style.css           # Estilização HUD tático com aceleração por GPU
│       └── js/
│           ├── webrtc.js           # Player WHEP WebRTC com auto-reconnect
│           ├── clock.js            # Relógio Modular Apple Watch Ultra (60 ticks SVG)
│           └── telemetry.js        # Consumidor de SSE e renderizador Tesla Solar Card
├── .env.example                    # Template de variáveis de ambiente
├── config.example.yaml             # Template de configuração de câmeras e cidades
├── go.mod
├── go.sum
└── README.md
```

---

## 4. Motor de Streaming WHEP e Resiliência RTSP

### 4.1. Ingestão RTSP e Passthrough H.264
* Utiliza pacotes do núcleo do `AlexxIT/go2rtc` integrados programaticamente na camada Go (`internal/stream`).
* As fontes RTSP são cadastradas na inicialização.
* O fluxo é estritamente **Zero-Copy Passthrough**: pacotes RTP chegam por RTSP e são direcionados diretamente aos canais SRTP do WebRTC sem re-encoding.
* Mantém uso de CPU do servidor < 2% e RAM < 35 MB.

### 4.2. Protocolo WHEP (`POST /api/whep/{camera_id}`)
1. O frontend gera uma `RTCPeerConnection` (`recvonly`) e produz a **SDP Offer**.
2. Envia a oferta via `POST /api/whep/{camera_id}` com `Content-Type: application/sdp`.
3. O servidor conecta a track de vídeo H.264 e devolve a **SDP Answer** com HTTP `201 Created`.
4. Latência fim a fim obtida no navegador: **< 200ms**.

### 4.3. Resiliência e Reconexão Silenciosa
* Se uma câmera cair (cabo desconectado ou reinicialização), o servidor mantém a rota viva e retenta a conexão RTSP em background.
* O player cliente (`webrtc.js`) detecta falha de conexão e reexecuta o handshake WHEP automaticamente com *exponential backoff*, sem necessidade de recarregar a página (`F5`).
* Durante a reconexão, o contêiner de vídeo preserva suas dimensões na grade com um overlay discreto de status (*"Reconectando..."*).

---

## 5. Hub de Telemetria e Provedores Externos

### 5.1. Huawei FusionSolar Kiosk REST API (`internal/solar/fusionsolar.go`)
* **Endpoint Consumido**:  
  `https://intl.fusionsolar.huawei.com/rest/pvms/web/kiosk/v1/station-kiosk-file?kk={TOKEN}`
* **Fluxo de Decodificação**:
  1. Requisição HTTP GET com cabeçalhos de navegador (`User-Agent`, `Referer: https://intl.fusionsolar.huawei.com/pvmswebsite/nologin/assets/build/cloud.html`).
  2. A resposta contém um objeto `{"success": true, "data": "{...}"}` onde `data` é uma string JSON codificada com HTML entities.
  3. O Go aplica `html.UnescapeString` e faz o parse da struct interna contendo:
     - `realTimePower`: Potência instantânea em kW.
     - `dailyEnergy`: Geração acumulada do dia em kWh.
     - `stationName` e endereço da planta.
     - Redução de CO₂ e árvores equivalentes.
  4. Suporte a arquivo de fallback local `dados_solar.json` caso a rede fique indisponível.
* **Ciclo de Polling**: A cada 5 minutos, com cache em memória.

### 5.2. Open-Meteo & Estimativa Fotovoltaica (`internal/weather/openmeteo.go`)
* **Endpoint Consumido**:  
  `https://api.open-meteo.com/v1/forecast?latitude={lat}&longitude={lon}&daily=temperature_2m_max,temperature_2m_min,shortwave_radiation_sum,weathercode&current_weather=true&timezone=auto`
* **Cálculos Físicos**:
  - Conversão de Radiação Solar: $\text{kWh/m²} = \frac{\text{shortwave\_radiation\_sum (MJ)}}{3.6}$.
  - Estimativa de Geração Teórica: $\sim 20\%$ de eficiência padrão ($\text{kWh/m²} \times 0.20$).
  - Classificação de Potencial Solar:
    - $\ge 6.0\text{ kWh/m²}$: Excelente (`#FFB300`)
    - $\ge 4.5\text{ kWh/m²}$: Bom (`#FFA000`)
    - $\ge 3.0\text{ kWh/m²}$: Moderado (`#FB8C00`)
    - $< 3.0\text{ kWh/m²}$: Baixo (`#90A4AE`)
* **Mapeamento Climático**: Tradução de códigos WMO para descrições em pt-BR e ícones HUD.
* **Ciclo de Polling**: A cada 30 minutos, com cache atômico em memória.

### 5.3. SSE Broker (`internal/sse/broker.go`)
* Endpoint `GET /api/events`.
* Envio de payloads compactos em JSON:
  - Dados da Usina Solar
  - Previsão e Clima
  - Status e Uptime do Argus
* Heartbeat de 15 segundos (`: ping\n\n`) para prevenir expiração de sessões TCP.
* Despacho imediato do snapshot em cache para todo novo cliente que se conecta.

---

## 6. Frontend HUD Tático e Experiência Visual

### 6.1. Layout e Grid Dinâmico Adaptável
* Grade CSS responsiva adaptada ao número de câmeras configuradas:
  - **3 Câmeras (Padrão Comercial)**: Grid 2x2 preenchendo 100vw x 100vh:
    - Quadrante 1 (Top-Left): Câmera 1
    - Quadrante 2 (Top-Right): Câmera 2
    - Quadrante 3 (Bottom-Left): Câmera 3
    - Quadrante 4 (Bottom-Right): **HUD Tático**
  - **1 ou 2 Câmeras**: Reorganização flexível para equilibrar visualização dos vídeos com o HUD Tático.
* Acesso direto e limpo (sem tela de login) para inicialização autônoma em quiosques e TVs.
* Modo Tela Cheia via duplo clique ou tecla `F11`, com ocultação automática do cursor após 3 segundos.

### 6.2. Aceleração por Hardware e Isolamento de Renderização
* Contêineres de vídeo e HUD isolados em camadas dedicadas de GPU:
  ```css
  .camera-card, .hud-card {
    transform: translateZ(0);
    will-change: transform;
    isolation: isolate;
  }
  ```
* Atualização de telemetria estritamente pontual via `textContent` nos nós de texto folha, garantindo zero reflow na decodificação de vídeo.

### 6.3. Componentes HUD
1. **AppleModularClock**:
   - Mostrador com 60 marcadores de segundos desenhados matematicamente em SVG contornando um retângulo arredondado (`rx=22`).
   - Marcador luminoso (*head tick*) no segundo corrente com glow esmeralda.
   - Hora monumental com dois pontos piscantes, data em pt-BR e tag de período (*MANHÃ*, *TARDE*, *NOITE*).
2. **TeslaSolarCard**:
   - Destaque monumental para **GERAÇÃO AGORA (kW)** e **PRODUZIDO HOJE (kWh)**.
   - Beacon pulsante verde ("AO VIVO").
   - Faixa compacta no rodapé com previsão climática dos próximos 3 dias e pílulas de potencial solar.

---

## 7. Critérios de Aceite e Métricas de Validação

1. **Uso de CPU do Servidor**: Inferior a 2% em repouso com 3 streams de vídeo ativos.
2. **Consumo de Memória do Servidor**: Processo Go contido em menos de 35 MB de RAM.
3. **Fluidez de Vídeo**: 30 a 60 FPS cravados sem engasgos ou delay drift acumulado.
4. **Resiliência a Quedas**: Reconexão silenciosa e restauração do stream em < 3 segundos após reestabelecimento do sinal RTSP.
5. **Autonomia Local**: Funcionamento completo em redes isoladas sem dependência de CDNs externas.

---

## 8. Estratégia de Compilação Comercial

Compilação do binário estático único sem símbolos de depuração:
```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o argus cmd/server/main.go
```
O executável resultante terá entre 15MB e 25MB, ideal para instalação direta como serviço `systemd` no Linux ou serviço de segundo plano no Windows.
