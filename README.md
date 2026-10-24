# 🛡️ Argus — CCTV Command Hub em Go

O **Argus** é uma central de monitoramento CFTV (CCTV Command Hub) comercial, ultra-leve e de altíssimo desempenho, projetada para execução contínua 24/7 em **servidores Linux** (Ubuntu, Debian, Alpine, Fedora, Rocky, Raspberry Pi OS) e exibição em Smart TVs, quiosques e monitores de portaria.

---

## ⚡ Por Que o Argus é Diferente?

1. **Binário Único Estático para Linux**:
   - Compilado com `CGO_ENABLED=0` e sem dependências dinâmicas de sistema (libc/glibc).
   - Tamanho de apenas **~12 MB**, contendo o servidor Go, motor WebRTC e toda a interface web HUD embutida via `//go:embed`.
2. **Consumo Extremanente Baixo (Hardware Modesto)**:
   - **Servidor Linux**: CPU < 2% e RAM < 35 MB com múltiplos streams ativos (ideal para mini-PCs Celeron, VPS básicas ou Raspberry Pi 3/4/5).
   - **Zero Transcodificação**: Roteamento direto *Zero-Copy Passthrough* (RTP over RTSP ➔ RTP over WebRTC SRTP) em H.264.
3. **Telemetria Solar & Meteorológica em Tempo Real**:
   - Conexão nativa com a API REST de Quiosque da **Huawei FusionSolar** (potência em kW e geração em kWh).
   - Previsão de 3 dias e estimativa de radiação solar fotovoltaica (kWh/m²) via **Open-Meteo**.
   - Envio instantâneo ao navegador via Server-Sent Events (SSE).
4. **Interface Tática 100% Vanilla (Sem Frameworks)**:
   - Relógio Modular de Aviação/Apple Watch Ultra com 60 marcadores de segundos matemáticos em SVG.
   - Painel solar estilo Tesla com leitura facilitada para TVs a longa distância.
   - Camadas aceleradas por GPU (`transform: translateZ(0)` e `isolation: isolate`) garantindo 30 a 60 FPS cravados sem queda de desempenho.

---

## 🐧 Guia Definitivo de Implantação no Servidor Linux

Como o servidor de produção é **sempre Linux**, o projeto possui um script automatizado (`setup_service.sh`) que realiza todo o processo de preparação, testes, compilação e ativação como serviço do sistema no `systemd`.

---

### ⚡ Instalação Rápida em 1 Comando (`setup_service.sh`)

No terminal do seu servidor Linux, dentro da pasta do projeto, basta executar:

```bash
./setup_service.sh
```

**O que este script faz automaticamente por você:**
1. Cria os arquivos `.env` e `config.yaml` a partir dos modelos (caso ainda não existam).
2. Roda a suíte completa de testes unitários (`go test` e `go vet`) para garantir a integridade do código.
3. Compila o binário estático e otimizado (`argus`) com `CGO_ENABLED=0`.
4. Gera o arquivo de serviço `argus.service` configurado com seu usuário e caminho local.
5. Instala no `systemd`, habilita no boot do sistema e inicia o serviço.
6. Valida se o serviço está ativo e exibe a URL com o IP da sua rede para abrir na TV.

---

### 🛠️ Instalação Passo a Passo Manual (Opcional)

Se você estiver compilando diretamente no servidor Linux ou gerando o executável a partir de outra máquina (macOS / CI):

#### Para servidores Linux convencionais (x86_64 / Intel / AMD):
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o argus cmd/server/main.go
```

#### Para mini-PCs ARM ou Raspberry Pi 4/5 (Linux ARM64):
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o argus cmd/server/main.go
```

O arquivo gerado `argus` é 100% estático e não requer instalação do Go nem de bibliotecas no servidor de destino.

---

### 2. Estrutura Padrão no Linux (`/opt/argus`)

Recomenda-se instalar a aplicação no diretório `/opt/argus`:

```bash
# 1. Cria o diretório da aplicação
sudo mkdir -p /opt/argus

# 2. Copia o binário compilado
sudo cp argus /opt/argus/
sudo chmod +x /opt/argus/argus

# 3. Cria usuário de sistema dedicado (boa prática de segurança)
sudo useradd -r -s /bin/false argus || true
sudo chown -R argus:argus /opt/argus
```

---

### 3. Configuração de Variáveis de Ambiente (`.env`)

Crie o arquivo `/opt/argus/.env` com as credenciais sensíveis e senhas das câmeras:

```bash
sudo cp .env.example /opt/argus/.env
sudo chmod 600 /opt/argus/.env
sudo chown argus:argus /opt/argus/.env
```

Edite o arquivo `/opt/argus/.env`:

```ini
# Interface de escuta e porta
ARGUS_PORT=8080
ARGUS_BIND_ADDRESS=0.0.0.0

# Usina Solar (Huawei FusionSolar Kiosk)
FUSIONSOLAR_ENABLED=true
FUSIONSOLAR_TOKEN=EM6wrnyAhBtOFvD7rdNu045xGpwb1bGD
FUSIONSOLAR_KIOSK_URL=https://intl.fusionsolar.huawei.com/pvmswebsite/nologin/assets/build/cloud.html#/kiosk?kk=EM6wrnyAhBtOFvD7rdNu045xGpwb1bGD

# Câmeras RTSP (Usuário e senha protegidos no servidor)
CAM_1_RTSP=rtsp://admin:senha123@192.168.1.101:554/stream1
CAM_2_RTSP=rtsp://admin:senha123@192.168.1.102:554/stream1
CAM_3_RTSP=rtsp://admin:senha123@192.168.1.103:554/stream1
```

---

### 4. Configuração Declarativa de Câmeras (`config.yaml`)

Crie o arquivo `/opt/argus/config.yaml`:

```bash
sudo cp config.example.yaml /opt/argus/config.yaml
sudo chown argus:argus /opt/argus/config.yaml
```

Conteúdo de `/opt/argus/config.yaml`:

```yaml
server:
  port: 8080
  bind_address: "0.0.0.0"

cameras:
  - id: "cam_frente"
    name: "Câmera 01 — Frente / Portão"
    rtsp_url: "${CAM_1_RTSP}"

  - id: "cam_garagem"
    name: "Câmera 02 — Garagem / Entrada"
    rtsp_url: "${CAM_2_RTSP}"

  - id: "cam_fundos"
    name: "Câmera 03 — Fundos / Perímetro"
    rtsp_url: "${CAM_3_RTSP}"

solar:
  poll_interval_minutes: 5

weather:
  enabled: true
  latitude: -10.8778   # Exemplo: Ji-Paraná, RO
  longitude: -61.9514
  city_name: "Ji-Paraná, RO"
  poll_interval_minutes: 30
```

---

### 5. Configuração do Serviço no Linux (`systemd`)

Para garantir que o Argus inicie automaticamente no boot do servidor Linux e reinicie sozinho em caso de oscilação de rede:

Crie o arquivo de unidade `/etc/systemd/system/argus.service`:

```ini
[Unit]
Description=Argus CCTV Command Hub
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=argus
Group=argus
WorkingDirectory=/opt/argus
ExecStart=/opt/argus/argus -config /opt/argus/config.yaml
Restart=always
RestartSec=3
LimitNOFILE=65535

# Proteções de segurança no Linux
ProtectSystem=full
ProtectHome=true
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

Habilite e gerencie o serviço com os comandos do `systemd`:

```bash
# Recarrega as definições do systemd
sudo systemctl daemon-reload

# Habilita inicialização automática com o sistema
sudo systemctl enable argus

# Inicia o serviço
sudo systemctl start argus

# Verifica o status e saúde do processo
sudo systemctl status argus

# Monitora os logs em tempo real
sudo journalctl -u argus -f
```

---

### 6. Configuração de Firewall no Servidor Linux

Se o servidor Linux possuir firewall ativo:

#### No Ubuntu / Debian (`ufw`):
```bash
sudo ufw allow 8080/tcp comment 'Argus CCTV Command Hub'
sudo ufw reload
```

#### No CentOS / Rocky / Fedora (`firewalld`):
```bash
sudo firewall-cmd --permanent --add-port=8080/tcp
sudo firewall-cmd --reload
```

---

### 7. Como Acessar e Usar na TV ou Quiosque

1. Conecte a Smart TV, TV Box ou Raspberry Pi na mesma rede local do servidor Linux.
2. Abra o navegador da TV e acesse:
   ```text
   http://IP_DO_SERVIDOR_LINUX:8080
   ```
3. Pressione a tecla **F11** no teclado ou dê um **duplo clique** em qualquer área da tela para entrar no modo tela cheia de monitoramento contínuo.
4. O cursor do mouse desaparecerá automaticamente após 3 segundos de inatividade para exibição limpa.

---

### 💡 Dica: Inicialização Automática de Quiosque no Linux (Kiosk Mode)

Se você tiver um mini-PC Linux ou Raspberry Pi conectado diretamente à TV via cabo HDMI:

```bash
chromium-browser --kiosk --noerrdialogs --disable-infobars --check-for-update-interval=31536000 http://localhost:8080
```

---

## 🧪 Comandos de Teste e Validação

Para validar o código e verificar a integridade de todos os submódulos:

```bash
go test -v ./...
go vet ./...
```
