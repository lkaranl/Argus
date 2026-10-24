/**
 * ARGUS CCTV COMMAND HUB — Gerenciador de Vídeo WebRTC via WHEP
 * Conexão de baixa latência (<200ms) com autoplay forçado para Smart TVs (sem mouse/interação)
 * e auto-reconnect silencioso sem recarga de página.
 */

class WHEPPlayer {
  constructor(cameraId, videoElement, badgeElement, overlayElement, statusTextElement) {
    this.cameraId = cameraId;
    this.video = videoElement;
    this.badge = badgeElement;
    this.overlay = overlayElement;
    this.statusText = statusTextElement;

    this.pc = null;
    this.reconnectTimer = null;
    this.playRetryTimer = null;
    this.watchdogTimer = null;
    this.reconnectDelay = 2000;
    this.maxReconnectDelay = 15000;
    this.isConnecting = false;
    this.connectedAt = 0;
    this.lastTimeUpdate = 0;
    this.lastCurrentTime = -1;

    this.configureVideoElement();
    this.startWatchdog();
    this.connect();
  }

  /**
   * Configura atributos e listeners no elemento <video> para garantir
   * que navegadores de Smart TVs (WebOS, Tizen, Android TV) permitam autoplay sem interação.
   */
  configureVideoElement() {
    this.enforceMutedState();

    const triggerPlay = () => {
      this.tryPlay();
    };

    this.video.addEventListener("loadedmetadata", triggerPlay);
    this.video.addEventListener("loadeddata", triggerPlay);
    this.video.addEventListener("canplay", triggerPlay);
    this.video.addEventListener("canplaythrough", triggerPlay);

    this.video.addEventListener("playing", () => {
      this.stopPlayRetryLoop();
      this.lastTimeUpdate = Date.now();
      this.updateStatus("ONLINE", "", "online");
    });

    this.video.addEventListener("timeupdate", () => {
      if (this.video.currentTime > 0 && this.video.currentTime !== this.lastCurrentTime) {
        this.lastCurrentTime = this.video.currentTime;
        this.lastTimeUpdate = Date.now();
        this.updateStatus("ONLINE", "", "online");
      }
    });

    // Se a TV ou o navegador tentar pausar o vídeo automaticamente, retoma na hora
    this.video.addEventListener("pause", () => {
      if (this.video.srcObject && !document.hidden) {
        setTimeout(() => this.tryPlay(), 150);
      }
    });

    this.video.addEventListener("suspend", () => {
      if (this.video.paused && this.video.srcObject) {
        this.tryPlay();
      }
    });
  }

  enforceMutedState() {
    if (!this.video) return;
    this.video.muted = true;
    this.video.defaultMuted = true;
    this.video.volume = 0;
    this.video.autoplay = true;
    this.video.playsInline = true;
    this.video.controls = false;
    this.video.removeAttribute("controls");
    this.video.setAttribute("muted", "");
    this.video.setAttribute("autoplay", "");
    this.video.setAttribute("playsinline", "");
    this.video.setAttribute("webkit-playsinline", "");
  }

  tryPlay() {
    if (!this.video || !this.video.srcObject) return;
    this.enforceMutedState();

    const playPromise = this.video.play();
    if (playPromise !== undefined) {
      playPromise
        .then(() => {
          this.stopPlayRetryLoop();
          this.updateStatus("ONLINE", "", "online");
        })
        .catch(() => {
          // Agenda novas tentativas automáticas caso o navegador da TV ainda esteja inicializando o buffer
          this.startPlayRetryLoop();
        });
    }
  }

  startPlayRetryLoop() {
    if (this.playRetryTimer) return;
    this.playRetryTimer = setInterval(() => {
      if (!this.video || !this.video.srcObject) {
        this.stopPlayRetryLoop();
        return;
      }
      if (!this.video.paused && this.video.readyState >= 2) {
        this.stopPlayRetryLoop();
        this.updateStatus("ONLINE", "", "online");
        return;
      }
      this.enforceMutedState();
      this.video.play().then(() => {
        this.stopPlayRetryLoop();
        this.updateStatus("ONLINE", "", "online");
      }).catch(() => {});
    }, 400);
  }

  stopPlayRetryLoop() {
    if (this.playRetryTimer) {
      clearInterval(this.playRetryTimer);
      this.playRetryTimer = null;
    }
  }

  /**
   * Watchdog contínuo: garante que o vídeo nunca fique pausado na TV
   * e reconecta automaticamente se a sessão WebRTC ficar sem receber quadros.
   */
  startWatchdog() {
    if (this.watchdogTimer) clearInterval(this.watchdogTimer);
    this.watchdogTimer = setInterval(() => {
      if (this.video && this.video.srcObject && this.video.paused && !document.hidden) {
        this.tryPlay();
      }

      // Verifica se a conexão ficou congelada sem decodificar frames por mais de 12s
      if (
        this.pc &&
        this.pc.connectionState === "connected" &&
        this.connectedAt > 0 &&
        Date.now() - this.connectedAt > 12000
      ) {
        const noFramesYet = this.video.readyState < 2 || this.lastTimeUpdate === 0;
        const stalledFrames = this.lastTimeUpdate > 0 && Date.now() - this.lastTimeUpdate > 15000;
        if (noFramesYet || stalledFrames) {
          this.connectedAt = Date.now();
          this.lastTimeUpdate = Date.now();
          this.connect();
        }
      }
    }, 1500);
  }

  async connect() {
    if (this.isConnecting) return;
    this.isConnecting = true;
    this.stopPlayRetryLoop();

    this.updateStatus("CONECTANDO", "Negociando WHEP...", "connecting");

    if (this.pc) {
      try { this.pc.close(); } catch (e) {}
      this.pc = null;
    }

    try {
      this.pc = new RTCPeerConnection({
        iceServers: [{ urls: "stun:stun.l.google.com:19302" }]
      });

      this.pc.addTransceiver("video", { direction: "recvonly" });

      this.pc.ontrack = (event) => {
        if (event.track && event.track.kind !== "video") {
          return;
        }

        // Usa exclusivamente a track de vídeo (sem faixa de áudio) para nunca acionar bloqueio de autoplay em TVs
        const inboundStream = new MediaStream([event.track]);
        this.enforceMutedState();
        this.video.srcObject = inboundStream;
        this.enforceMutedState();

        event.track.onunmute = () => {
          this.tryPlay();
        };

        this.tryPlay();
        this.startPlayRetryLoop();
      };

      this.pc.onconnectionstatechange = () => {
        if (!this.pc) return;
        const state = this.pc.connectionState;
        if (state === "connected") {
          this.reconnectDelay = 2000;
          this.connectedAt = Date.now();
          this.tryPlay();
        } else if (state === "disconnected" || state === "failed" || state === "closed") {
          this.updateStatus("RECONECTANDO", "Sinal interrompido...", "offline");
          this.scheduleReconnect();
        }
      };

      const offer = await this.pc.createOffer();
      await this.pc.setLocalDescription(offer);

      const resp = await fetch(`/api/whep/${this.cameraId}`, {
        method: "POST",
        headers: { "Content-Type": "application/sdp" },
        body: offer.sdp,
      });

      if (!resp.ok) {
        throw new Error(`Servidor WHEP retornou status HTTP ${resp.status}`);
      }

      const answerSDP = await resp.text();
      await this.pc.setRemoteDescription({
        type: "answer",
        sdp: answerSDP,
      });

      this.isConnecting = false;
      this.tryPlay();
    } catch (err) {
      this.isConnecting = false;
      this.updateStatus("SEM SINAL", "Tentando reconectar...", "offline");
      this.scheduleReconnect();
    }
  }

  updateStatus(badgeText, overlayMsg, stateClass) {
    if (this.badge) {
      this.badge.textContent = badgeText;
      this.badge.className = `cam-badge ${stateClass}`;
    }

    if (this.statusText && overlayMsg) {
      this.statusText.textContent = overlayMsg;
    }

    if (this.overlay) {
      if (stateClass === "online") {
        this.overlay.classList.add("hidden");
      } else {
        this.overlay.classList.remove("hidden");
      }
    }
  }

  scheduleReconnect() {
    if (this.reconnectTimer) return;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.reconnectDelay = Math.min(this.reconnectDelay * 1.5, this.maxReconnectDelay);
      this.connect();
    }, this.reconnectDelay);
  }
}

// Inicialização automática de todas as câmeras encontradas no DOM
document.addEventListener("DOMContentLoaded", () => {
  const cards = document.querySelectorAll(".camera-card");
  window.argusPlayers = {};

  cards.forEach((card) => {
    const camId = card.getAttribute("data-camera-id");
    if (!camId) return;

    const video = document.getElementById(`video-${camId}`);
    const badge = document.getElementById(`cam-badge-${camId}`);
    const overlay = document.getElementById(`overlay-${camId}`);
    const statusText = document.getElementById(`status-text-${camId}`);

    if (video) {
      window.argusPlayers[camId] = new WHEPPlayer(camId, video, badge, overlay, statusText);
    }
  });

  // Retoma reprodução automática de todas as câmeras ao voltar do standby da TV ou qualquer tecla do controle remoto
  const resumeAllCameras = () => {
    if (!window.argusPlayers) return;
    Object.values(window.argusPlayers).forEach((player) => {
      player.tryPlay();
    });
  };

  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) resumeAllCameras();
  });
  window.addEventListener("focus", resumeAllCameras);
  window.addEventListener("pageshow", resumeAllCameras);
  document.addEventListener("keydown", resumeAllCameras);
  document.addEventListener("click", resumeAllCameras);
  document.addEventListener("touchstart", resumeAllCameras, { passive: true });

  // Controle de Tela Cheia (F11, duplo clique ou botão dedicado)
  const btnFullscreen = document.getElementById("btn-fullscreen");
  const toggleFullscreen = () => {
    if (!document.fullscreenElement) {
      document.documentElement.requestFullscreen().catch(() => {});
    } else {
      document.exitFullscreen().catch(() => {});
    }
  };

  if (btnFullscreen) {
    btnFullscreen.addEventListener("click", toggleFullscreen);
  }

  document.addEventListener("dblclick", (e) => {
    if (e.target.tagName !== "BUTTON") {
      toggleFullscreen();
    }
  });

  // Ocultação automática de cursor após 3 segundos no modo tela cheia
  let cursorTimeout;
  document.addEventListener("mousemove", () => {
    document.body.classList.remove("fullscreen-active");
    clearTimeout(cursorTimeout);
    if (document.fullscreenElement) {
      cursorTimeout = setTimeout(() => {
        document.body.classList.add("fullscreen-active");
      }, 3000);
    }
  });
});
