#!/usr/bin/env bash
# ==============================================================================
# Script de Instalação, Compilação e Ativação Automática do Argus no systemd
# ==============================================================================

set -e

# Cores para saída no terminal
RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
BOLD='\033[1m'
NC='\033[0m' # Sem cor

echo -e "${CYAN}==================================================================${NC}"
echo -e "${CYAN}   🛡️  ARGUS — Instalação e Ativação do Serviço Linux (24/7)     ${NC}"
echo -e "${CYAN}==================================================================${NC}"

# 1. Detecta usuário real (caso executado com sudo) e diretório
REAL_USER="${SUDO_USER:-$USER}"
REAL_GROUP="$(id -gn "$REAL_USER" 2>/dev/null || echo "$REAL_USER")"
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo -e "📂 Diretório do projeto: ${YELLOW}$REPO_DIR${NC}"
echo -e "👤 Usuário do serviço:   ${YELLOW}$REAL_USER ($REAL_GROUP)${NC}"

# 2. Configura arquivos base (.env e config.yaml) caso não existam
if [ ! -f "$REPO_DIR/.env" ]; then
    echo -e "📝 Criando arquivo de ambiente ${YELLOW}.env${NC} a partir do modelo..."
    cp "$REPO_DIR/.env.example" "$REPO_DIR/.env"
    chmod 600 "$REPO_DIR/.env"
    chown "$REAL_USER:$REAL_GROUP" "$REPO_DIR/.env" 2>/dev/null || true
    echo -e "${GREEN}   ✓ .env criado com sucesso (ajuste suas credenciais quando necessário).${NC}"
fi

if [ ! -f "$REPO_DIR/config.yaml" ]; then
    echo -e "📝 Criando arquivo de configuração ${YELLOW}config.yaml${NC} a partir do modelo..."
    cp "$REPO_DIR/config.example.yaml" "$REPO_DIR/config.yaml"
    chown "$REAL_USER:$REAL_GROUP" "$REPO_DIR/config.yaml" 2>/dev/null || true
    echo -e "${GREEN}   ✓ config.yaml criado com sucesso.${NC}"
fi

# 3. Cria diretório de persistência de dados caso não exista
mkdir -p "$REPO_DIR/data"
chown "$REAL_USER:$REAL_GROUP" "$REPO_DIR/data" 2>/dev/null || true

# 4. Execução de testes de integridade antes do build
echo -e "\n🧪 Executando suíte de testes unitários do Argus..."
if command -v go >/dev/null 2>&1; then
    (cd "$REPO_DIR" && go test -short ./... && go vet ./...)
    echo -e "${GREEN}   ✓ Todos os testes unitários passaram com sucesso!${NC}"
else
    echo -e "${YELLOW}   ⚠️  Go não encontrado no PATH atual. Prosseguindo se o binário já existir...${NC}"
fi

# 5. Compilação do binário estático comercial
echo -e "\n🔨 Compilando binário estático e otimizado (CGO_ENABLED=0)..."
if command -v go >/dev/null 2>&1; then
    (cd "$REPO_DIR" && CGO_ENABLED=0 go build -ldflags="-s -w" -o argus cmd/server/main.go)
    chmod +x "$REPO_DIR/argus"
    echo -e "${GREEN}   ✓ Binário estático 'argus' compilado com sucesso (~$(du -h "$REPO_DIR/argus" | awk '{print $1}')).${NC}"
elif [ -f "$REPO_DIR/argus" ]; then
    chmod +x "$REPO_DIR/argus"
    echo -e "${GREEN}   ✓ Binário existente 'argus' localizado.${NC}"
else
    echo -e "${RED}   ✗ Erro: O Go não está instalado e nenhum binário 'argus' foi encontrado.${NC}"
    exit 1
fi

# 6. Geração dinâmica do arquivo systemd
SERVICE_FILE="/etc/systemd/system/argus.service"
TEMP_SERVICE="$(mktemp)"

cat <<EOF > "$TEMP_SERVICE"
[Unit]
Description=Argus (CCTV Command Hub) - Central de Monitoramento e Telemetria 24/7
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$REAL_USER
Group=$REAL_GROUP
WorkingDirectory=$REPO_DIR
EnvironmentFile=-$REPO_DIR/.env
ExecStart=$REPO_DIR/argus -config $REPO_DIR/config.yaml
Restart=always
RestartSec=3s
RuntimeMaxSec=48h
LimitNOFILE=65535
SyslogIdentifier=argus-cctv
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

# Instala o serviço diretamente no systemd sem sujar o controle de versão do git
echo -e "\n📋 Instalando arquivo de serviço customizado no systemd (${SERVICE_FILE})..."

if [ "$EUID" -ne 0 ]; then
    sudo cp "$TEMP_SERVICE" "$SERVICE_FILE"
    sudo chmod 644 "$SERVICE_FILE"
    rm -f "$TEMP_SERVICE"

    echo -e "🔄 Recarregando configurações do systemd..."
    sudo systemctl daemon-reload

    echo -e "⚡ Habilitando inicialização automática no boot..."
    sudo systemctl enable argus.service

    echo -e "🌐 Aplicando otimizações de rede no Kernel (BBR para Wi-Fi/RF e buffers WebRTC)..."
    sudo modprobe tcp_bbr 2>/dev/null || true
    sudo sysctl -w net.core.rmem_max=8388608 >/dev/null 2>&1 || true
    sudo sysctl -w net.core.wmem_max=8388608 >/dev/null 2>&1 || true
    sudo sysctl -w net.ipv4.tcp_congestion_control=bbr >/dev/null 2>&1 || true

    echo -e "🚀 Iniciando serviço Argus..."
    sudo systemctl restart argus.service
else
    cp "$TEMP_SERVICE" "$SERVICE_FILE"
    chmod 644 "$SERVICE_FILE"
    rm -f "$TEMP_SERVICE"

    echo -e "🔄 Recarregando configurações do systemd..."
    systemctl daemon-reload

    echo -e "⚡ Habilitando inicialização automática no boot..."
    systemctl enable argus.service

    echo -e "🌐 Aplicando otimizações de rede no Kernel (BBR para Wi-Fi/RF e buffers WebRTC)..."
    modprobe tcp_bbr 2>/dev/null || true
    sysctl -w net.core.rmem_max=8388608 >/dev/null 2>&1 || true
    sysctl -w net.core.wmem_max=8388608 >/dev/null 2>&1 || true
    sysctl -w net.ipv4.tcp_congestion_control=bbr >/dev/null 2>&1 || true

    echo -e "🚀 Iniciando serviço Argus..."
    systemctl restart argus.service
fi

echo -e "⏳ Verificando status do serviço..."
sleep 2

# 7. Verificação de status
if systemctl is-active --quiet argus; then
    SERVER_IP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{print $7}' | head -n 1)
    if [ -z "$SERVER_IP" ]; then
        SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
    fi
    if [ -z "$SERVER_IP" ]; then
        SERVER_IP="localhost"
    fi

    echo -e "\n${GREEN}==================================================================${NC}"
    echo -e "${GREEN}  ✓ Argus CCTV Command Hub iniciado e operando com sucesso!       ${NC}"
    echo -e "${GREEN}==================================================================${NC}"
    echo -e "🌐 Acesse no navegador da TV ou computador:"
    echo -e "   • Painel Principal:  ${CYAN}${BOLD}http://${SERVER_IP}:8080${NC}"
    echo -e "   • Status da API:     ${CYAN}http://${SERVER_IP}:8080/api/status${NC}"
    echo -e "   • Câmeras Ativas:    ${CYAN}http://${SERVER_IP}:8080/api/cameras${NC}\n"
    echo -e "📜 Comandos úteis para gerenciamento no Linux:"
    echo -e "   • Ver status:        ${YELLOW}systemctl status argus${NC}"
    echo -e "   • Reiniciar serviço: ${YELLOW}sudo systemctl restart argus${NC}"
    echo -e "   • Parar serviço:     ${YELLOW}sudo systemctl stop argus${NC}"
    echo -e "   • Acompanhar logs:   ${YELLOW}journalctl -u argus -f${NC}\n"
else
    echo -e "\n${RED}==================================================================${NC}"
    echo -e "${RED}  ✗ O serviço falhou ao iniciar. Verifique os logs abaixo:       ${NC}"
    echo -e "${RED}==================================================================${NC}"
    journalctl -u argus -n 25 --no-pager
    exit 1
fi
