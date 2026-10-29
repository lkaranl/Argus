# 🍓 Guia de Kiosk: Raspberry Pi 4 + labwc (Wayland) + Mesa V3D

Este guia documenta as configurações e otimizações de aceleração gráfica por hardware para rodar o **Argus** em exibição contínua 24/7 no **Raspberry Pi 4 Model B (Debian 13 trixie aarch64)** com compositor **`labwc` (Wayland)** conectado à TV Philips (1360x768).

---

## ⚡ Por que essas configurações são essenciais no Pi 4?

1. **Evita a camada XWayland**:
   Rodar o navegador nativamente com `--ozone-platform=wayland` economiza mais de **150 MB de memória RAM** e elimina a cópia redundante de buffers entre o X11 e o Wayland.
2. **Habilita Direct Scanout no Mesa V3D**:
   Com o CSS otimizado do Argus (`contain: strict`, `transform: translate3d(0,0,0)` e overlays com `display: none`), o `labwc` repassa as superfícies de vídeo diretamente para o hardware display controller do Broadcom BCM2711 sem gastar ciclos de shader da GPU VideoCore VI.
3. **Reduz uso de Swap e Temperatura**:
   Desativa recursos supérfluos do Chromium (tradução, telemetria de atualizações, extensões) para evitar *swap thrashing* no cartão SD / SSD.

---

## 🛠️ Como Configurar o Autostart no `labwc`

No seu Raspberry Pi 4, edite ou crie o arquivo de inicialização do `labwc`:

```bash
mkdir -p ~/.config/labwc
nano ~/.config/labwc/autostart
```

Adicione o comando com as flags otimizadas:

```bash
# Desativa protetor de tela e suspensão do monitor
wlr-randr --output HDMI-A-1 --mode 1360x768@60Hz 2>/dev/null || true

# Inicia o Chromium em modo Kiosk Wayland nativo para o Argus
chromium --kiosk \
         --ozone-platform=wayland \
         --enable-features=OverlayScrollbar,VaapiVideoDecoder \
         --enable-gpu-rasterization \
         --enable-zero-copy \
         --disable-features=TranslateUI \
         --noerrdialogs \
         --disable-infobars \
         --disable-session-crashed-bubble \
         --check-for-update-interval=31536000 \
         --autoplay-policy=no-user-gesture-required \
         http://127.0.0.1:8080 &
```

> **Dica**: Se o Argus estiver rodando em outro servidor da rede, substitua `http://127.0.0.1:8080` pelo IP do servidor (ex: `http://192.168.68.X:8080`).

---

## 🛡️ Ajustes do Sistema para Evitar Travamentos por Pouca Memória

Como o Pi 4 possui 2 GB de RAM e já utiliza swap:

1. **ZRAM em vez de Swap em Cartão SD / Disco**:
   Instale o `zram-tools` no Debian para criar swap comprimido diretamente na RAM, muito mais rápido e sem desgastar a memória flash:
   ```bash
   sudo apt install -y zram-tools
   ```
2. **Alocação de Memória GPU (config.txt)**:
   Em sistemas Wayland modernos de 64 bits com driver KMS (`vc4-kms-v3d`), a memória de vídeo é alocada dinamicamente pelo kernel. Mantenha em `/boot/firmware/config.txt`:
   ```ini
   dtoverlay=vc4-kms-v3d
   gpu_mem=128
   ```
3. **Pacote de Fontes de Emoji do Sistema**:
   Em instalações mínimas do Raspberry Pi OS / Debian Kiosk, o suporte a emojis do Chromium depende do pacote de fontes:
   ```bash
   sudo apt install -y fonts-noto-color-emoji && fc-cache -f
   ```

---

## 📹 Substream sob Demanda (Redução Drástica de CPU)

### Como Funciona
Na grade 2x2 padrão, cada câmera ocupa aproximadamente 680x384 na tela 1360x768 da TV.
- **Na Grade**: O Argus solicita automaticamente o canal secundário (`substream_url`, ex: 640x360 ou 720p). Isso reduz a carga de decodificação na CPU Cortex-A72 em mais de 60%.
- **Em Tela Cheia**: Ao dar um **duplo clique** ou pressionar **Enter** em qualquer câmera, ela se expande para tela cheia e comuta instantaneamente para o **Mainstream** em alta resolução (1080p/2K).
- **Para Fechar**: Clique no botão flutuante **✕ FECHAR (ESC)**, pressione **Escape**, **Backspace** ou dê um duplo clique novamente. O Argus reverte para o substream e restaura a grade 2x2.

### Como Configurar no `config.yaml`:

```yaml
cameras:
  - id: "cam_frente"
    name: "Portão Frente"
    rtsp_url: "rtsp://admin:senha@192.168.68.53:554/stream1"       # Mainstream (1080p/2K)
    substream_url: "rtsp://admin:senha@192.168.68.53:554/stream2" # Substream (640x360 / D1)
```

> **Fallback Transparente**: Se uma câmera não possuir `substream_url` configurado, o Argus utiliza o stream principal normalmente sem interrupções.
