# 🛡️ Argus — CCTV Command Hub em Go

O **Argus** é uma central de monitoramento CFTV (CCTV Command Hub) comercial, ultra-leve e de altíssimo desempenho, voltada para exibição ininterrupta 24/7 em Smart TVs, monitores de portaria, centrais de segurança e quiosques operacionais.

---

## ⚡ Destaques de Desempenho e Arquitetura

1. **Binário Único Estático**: Distribuído como um único arquivo compilado em Go (`CGO_ENABLED=0`), pesando apenas **~12 MB**, contendo o backend e todo o frontend embutido via `//go:embed`.
2. **Consumo Mínimo de Recursos**:
   - **Servidor**: Consumo de CPU < 2% e RAM < 35 MB em repouso com streams ativos, ideal para mini-PCs Celeron, Raspberry Pi ou servidores básicos sem placa de vídeo.
   - **Cliente (Navegador)**: Extrai o FPS máximo nativo das câmeras (30 a 60 FPS cravados) através de aceleração por hardware nativa da GPU/VPU.
3. **Zero Transcodificação**: Roteamento estrito *Zero-Copy Passthrough* (RTP over RTSP ➔ RTP over WebRTC SRTP) com codec **H.264**.
4. **Telemetria Integrada em Tempo Real via SSE**:
   - **Usina Solar Fotovoltaica (Huawei FusionSolar Kiosk)**: Coleta potência instantânea em kW e geração do dia em kWh diretamente do quiosque público oficial da Huawei.
   - **Clima e Estimativa Fotovoltaica (Open-Meteo)**: Previsão de 3 dias, cálculo de radiação em kWh/m² e potencial solar.
5. **Frontend 100% Vanilla (Sem Frameworks)**:
   - Relógio Modular estilo Apple Watch Ultra com mostrador periférico de 60 marcadores de segundos desenhados matematicamente em SVG vetorial.
   - Painel Solar estilo Tesla com métricas monumentais legíveis a longa distância.
   - Isolamento de renderização da GPU (`transform: translateZ(0)` e `isolation: isolate`) garantindo que as telemetrias e o relógio não provoquem *reflow* ou perda de frames nos vídeos.
   - Modo Tela Cheia automático com duplo clique ou tecla `F11`.

---

## 📂 Estrutura do Projeto

```text
argus/
├── cmd/
│   └── server/
│       ├── main.go               # Ponto de entrada e graceful shutdown
│       └── server_test.go        # Testes de integração de rotas HTTP
├── internal/
│   ├── config/
│   │   ├── config.go             # Leitor de config.yaml e binding com .env
│   │   └── config_test.go        # Teste de interpolação de variáveis
│   ├── stream/
│   │   ├── engine.go             # Gerenciador de tracks WebRTC Pion
│   │   ├── whep.go               # Endpoint HTTP WHEP (SDP Offer/Answer)
│   │   └── whep_test.go          # Testes unitários do handshake WHEP
│   ├── sse/
│   │   ├── broker.go             # Broker Pub/Sub thread-safe para Server-Sent Events
│   │   └── broker_test.go        # Testes de broadcast e clientes lentos
│   ├── solar/
│   │   ├── fusionsolar.go        # Cliente Kiosk REST da Huawei FusionSolar
│   │   └── fusionsolar_test.go   # Testes com payload HTML-escaped da Huawei
│   ├── weather/
│   │   ├── openmeteo.go          # Cliente Open-Meteo com estimativa solar
│   │   └── openmeteo_test.go     # Testes de conversão física de radiação
│   └── telemetry/
│       ├── manager.go            # Orquestrador de background e agregação de dados
│       └── manager_test.go       # Testes de ciclo de vida e despacho
├── web/
│   ├── embed.go                  # Diretiva //go:embed para empacotar o frontend
│   ├── embed_test.go             # Validação de integridade dos arquivos estáticos
│   └── static/
│       ├── index.html            # Shell do Command Hub e contêineres de vídeo
│       ├── css/
│       │   └── style.css         # Estilo HUD tático acelerado por GPU
│       └── js/
│           ├── clock.js          # AppleModularClock de alta precisão (SVG)
│           ├── webrtc.js         # Negociação WHEP com auto-reconnect
│           └── telemetry.js      # Consumidor de SSE com atualização via textContent
├── .env.example                  # Template de segredos e credenciais
├── config.example.yaml           # Exemplo de configuração declarativa
├── go.mod
├── go.sum
└── README.md
```

---

## 🛠️ Configuração e Instalação

### 1. Configurar Variáveis de Ambiente (`.env`)

Crie seu arquivo `.env` a partir do modelo `.env.example`:

```bash
cp .env.example .env
```

Edite o `.env` com suas credenciais sensíveis (senhas das câmeras e token da usina solar):

```ini
ARGUS_PORT=8080
ARGUS_BIND_ADDRESS=0.0.0.0

# Usina Solar (Huawei FusionSolar Kiosk)
FUSIONSOLAR_ENABLED=true
FUSIONSOLAR_TOKEN=SEU_TOKEN_AQUI
FUSIONSOLAR_KIOSK_URL=https://intl.fusionsolar.huawei.com/pvmswebsite/nologin/assets/build/cloud.html#/kiosk?kk=SEU_TOKEN_AQUI

# Câmeras CFTV
CAM_1_RTSP=rtsp://admin:senha123@192.168.1.101:554/stream1
CAM_2_RTSP=rtsp://admin:senha123@192.168.1.102:554/stream1
CAM_3_RTSP=rtsp://admin:senha123@192.168.1.103:554/stream1
```

### 2. Configurar Topologia (`config.yaml`)

Crie seu arquivo `config.yaml` a partir do modelo `config.example.yaml`:

```bash
cp config.example.yaml config.yaml
```

---

## 🧪 Testes e Verificação

Para rodar todos os testes unitários e de integração:

```bash
go test -v ./...
go vet ./...
```

---

## 📦 Compilação Comercial Estática

Para compilar o binário estático e otimizado sem símbolos de depuração:

```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o argus cmd/server/main.go
```

O binário `argus` resultante estará pronto para ser copiado para qualquer máquina Linux/macOS ou distribuído comercialmente.

---

## 🚀 Implantação como Serviço de Sistema (Linux `systemd`)

Para executar o Argus continuamente em segundo plano em mini-PCs e servidores Linux:

Crie o arquivo `/etc/systemd/system/argus.service`:

```ini
[Unit]
Description=Argus CCTV Command Hub
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/argus
ExecStart=/opt/argus/argus -config /opt/argus/config.yaml
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

Ative e inicie o serviço:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now argus
```

Acesse o painel em qualquer TV ou navegador na rede local:
```
http://IP_DO_SERVIDOR:8080
```
