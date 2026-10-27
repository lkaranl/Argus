/**
 * ARGUS CCTV COMMAND HUB — Gerenciador de Vídeo WebRTC via WHEP
 * Conexão de baixa latência (<200ms) com autoplay forçado para Smart TVs (sem mouse/interação)
 * e auto-reconnect silencioso sem recarga de página.
 */

class WHEPPlayer {
  constructor(cameraId, videoElement, badgeElement, overlayElement, statusTextElement, fpsBadgeElement) {
    this.cameraId = cameraId;
    this.video = videoElement;
    this.badge = badgeElement;
    this.overlay = overlayElement;
    this.statusText = statusTextElement;
    this.fpsBadge = fpsBadgeElement;
    this.fpsValEl = fpsBadgeElement ? fpsBadgeElement.querySelector(".fps-val") : null;
    this.streamProfile = "sub"; // Inicia por padrão no substream leve para a grade 2x2

    this.pc = null;
    this.inboundStream = null;
    this.inboundTrack = null;
    this.reconnectTimer = null;
    this.playRetryTimer = null;
    this.watchdogTimer = null;
    this.fpsTimer = null;
    this.reconnectDelay = 1200;
    this.maxReconnectDelay = 3500;
    this.isConnecting = false;
    this.playPendingUntil = 0;
    this.connectedAt = 0;
    this.lastTimeUpdate = 0;
    this.lastCurrentTime = -1;
    this.lastDecodedFrames = -1;
    this.lastFrameProgressAt = 0;

    // Contadores para cálculo de FPS a cada 1s
    this.rvfcFrames = 0;
    this.rvfcActive = false;
    this.prevTotalFrames = 0;
    this.prevRtcDecodedFrames = 0;
    this.prevFpsTimestamp = performance.now();
    this.currentFps = 0;

    this.configureVideoElement();
    this.startFpsMonitor();
    this.startWatchdog();
    this.connect();
  }

  /**
   * Configura atributos e listeners no elemento <video> para garantir
   * que navegadores de Smart TVs (WebOS, Tizen, Android TV) e Linux Kiosks
   * permitam autoplay 100% automático sem mouse/interação.
   */
  configureVideoElement() {
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

    const onMediaReady = () => {
      if (!this.video.paused) {
        this.stopPlayRetryLoop();
        this.updateStatus("ONLINE", "", "online");
      } else if (this.video.readyState >= 1) {
        this.playPendingUntil = 0;
        this.tryPlay();
      }
    };

    this.video.addEventListener("loadedmetadata", onMediaReady);
    this.video.addEventListener("loadeddata", onMediaReady);
    this.video.addEventListener("canplay", onMediaReady);
    this.video.addEventListener("canplaythrough", onMediaReady);

    this.video.addEventListener("playing", () => {
      this.lastTimeUpdate = Date.now();
      this.lastFrameProgressAt = Date.now();
      this.stopPlayRetryLoop();
      this.updateStatus("ONLINE", "", "online");
    });

    this.video.addEventListener("timeupdate", () => {
      if (this.video.currentTime > 0 && this.video.currentTime !== this.lastCurrentTime) {
        this.lastCurrentTime = this.video.currentTime;
        this.lastTimeUpdate = Date.now();
        this.lastFrameProgressAt = Date.now();
        this.stopPlayRetryLoop();
        this.updateStatus("ONLINE", "", "online");
      }
    });

    // Se a TV ou o navegador tentar pausar o vídeo automaticamente após já ter carregado metadados, retoma
    this.video.addEventListener("pause", () => {
      if (this.video.srcObject && this.video.readyState >= 1 && !document.hidden) {
        this.playPendingUntil = 0;
        setTimeout(() => this.tryPlay(), 100);
      }
    });
  }

  enforceMutedState() {
    if (!this.video) return;
    if (!this.video.muted) this.video.muted = true;
    if (!this.video.defaultMuted) this.video.defaultMuted = true;
    if (this.video.volume !== 0) this.video.volume = 0;
    if (!this.video.autoplay) this.video.autoplay = true;
    if (!this.video.playsInline) this.video.playsInline = true;
  }

  isVideoRendering() {
    if (!this.video || !this.video.srcObject || this.video.paused) return false;
    return (
      this.video.readyState >= 2 &&
      (this.getDecodedFrameCount() > 0 || this.video.currentTime > 0 || this.rvfcFrames > 0)
    );
  }

  tryPlay(fromUserGesture = false) {
    if (!this.video || !this.video.srcObject) return;
    this.enforceMutedState();

    // Se o vídeo já não está pausado (autoplay nativo do Safari/Firefox em ação),
    // NUNCA chame video.play() novamente para não cancelar o m_autoPlay interno do WebKit/Gecko
    if (!this.video.paused) {
      if (this.video.readyState >= 1) {
        this.stopPlayRetryLoop();
        this.updateStatus("ONLINE", "", "online");
      }
      return;
    }

    // Nunca chame video.play() enquanto readyState === 0 (antes de loadedmetadata):
    // no Safari e no Firefox, chamar play() antes do MediaStream receber o primeiro frame
    // lança NotAllowedError e desativa o autoplay nativo do elemento <video>
    if (!fromUserGesture && this.video.readyState < 1) {
      return;
    }

    const now = Date.now();
    if (!fromUserGesture && now < this.playPendingUntil) return;
    this.playPendingUntil = now + 300;

    try {
      const playPromise = this.video.play();
      if (playPromise !== undefined) {
        playPromise
          .then(() => {
            this.playPendingUntil = 0;
            this.stopPlayRetryLoop();
            this.updateStatus("ONLINE", "", "online");
          })
          .catch(() => {
            this.playPendingUntil = 0;
            this.startPlayRetryLoop();
          });
      } else {
        this.playPendingUntil = 0;
        this.updateStatus("ONLINE", "", "online");
      }
    } catch (e) {
      this.playPendingUntil = 0;
      this.startPlayRetryLoop();
    }
  }

  startPlayRetryLoop() {
    if (this.playRetryTimer) return;
    this.playRetryTimer = setInterval(() => {
      if (!this.video || !this.video.srcObject) {
        this.stopPlayRetryLoop();
        return;
      }
      if (!this.video.paused && this.video.readyState >= 1) {
        this.stopPlayRetryLoop();
        this.updateStatus("ONLINE", "", "online");
        return;
      }
      if (this.video.readyState >= 1 && this.video.paused) {
        this.tryPlay();
      }
    }, 400);
  }

  stopPlayRetryLoop() {
    if (this.playRetryTimer) {
      clearInterval(this.playRetryTimer);
      this.playRetryTimer = null;
    }
  }

  /**
   * Retorna o total de quadros efetivamente decodificados pelo hardware da TV.
   */
  getDecodedFrameCount() {
    if (!this.video) return 0;
    if (typeof this.video.getVideoPlaybackQuality === "function") {
      const q = this.video.getVideoPlaybackQuality();
      if (q && typeof q.totalVideoFrames === "number" && q.totalVideoFrames > 0) {
        return q.totalVideoFrames;
      }
    }
    if (typeof this.video.webkitDecodedFrameCount === "number" && this.video.webkitDecodedFrameCount > 0) {
      return this.video.webkitDecodedFrameCount;
    }
    if (typeof this.video.mozPresentedFrames === "number" && this.video.mozPresentedFrames > 0) {
      return this.video.mozPresentedFrames;
    }
    if (typeof this.video.mozDecodedFrames === "number" && this.video.mozDecodedFrames > 0) {
      return this.video.mozDecodedFrames;
    }
    return 0;
  }

  armVideoFrameCallback() {
    if (!this.video || typeof this.video.requestVideoFrameCallback !== "function" || this.rvfcActive) {
      return;
    }
    this.rvfcActive = true;
    const onFrame = () => {
      this.rvfcFrames++;
      if (this.video) {
        this.video.requestVideoFrameCallback(onFrame);
      } else {
        this.rvfcActive = false;
      }
    };
    this.video.requestVideoFrameCallback(onFrame);
  }

  /**
   * Monitora e atualiza o indicador discreto de FPS a cada 1 segundo.
   * Verde (fps-active) quando >= 1 FPS; Vermelho (fps-zero) quando 0 FPS.
   */
  startFpsMonitor() {
    this.armVideoFrameCallback();

    if (this.fpsTimer) clearInterval(this.fpsTimer);
    this.fpsTimer = setInterval(async () => {
      const now = performance.now();
      const elapsedSec = Math.max((now - this.prevFpsTimestamp) / 1000, 0.5);
      this.prevFpsTimestamp = now;

      // 1. Contagem via requestVideoFrameCallback
      const rvfcFps = Math.round(this.rvfcFrames / elapsedSec);
      this.rvfcFrames = 0;

      // 2. Contagem via getVideoPlaybackQuality / webkitDecodedFrameCount / mozPresentedFrames
      let qualityFps = 0;
      const total = this.getDecodedFrameCount();
      if (total > 0) {
        if (this.prevTotalFrames > 0 && total >= this.prevTotalFrames) {
          qualityFps = Math.round((total - this.prevTotalFrames) / elapsedSec);
        }
        this.prevTotalFrames = total;
      }

      // 3. Contagem nativa do motor WebRTC C++ (inbound-rtp framesPerSecond / framesDecoded)
      let rtcFps = 0;
      if (this.pc) {
        try {
          const stats = await this.pc.getStats();
          stats.forEach((report) => {
            if (report.type === "inbound-rtp" && (report.kind === "video" || report.mediaType === "video")) {
              if (typeof report.framesPerSecond === "number" && report.framesPerSecond > 0) {
                rtcFps = Math.max(rtcFps, Math.round(report.framesPerSecond));
              }
              if (typeof report.framesDecoded === "number" && report.framesDecoded > 0) {
                if (this.prevRtcDecodedFrames > 0 && report.framesDecoded >= this.prevRtcDecodedFrames) {
                  const deltaFps = Math.round((report.framesDecoded - this.prevRtcDecodedFrames) / elapsedSec);
                  rtcFps = Math.max(rtcFps, deltaFps);
                }
                this.prevRtcDecodedFrames = report.framesDecoded;
              }
            }
          });
        } catch (e) {}
      }

      // Se o motor WebRTC já está decodificando quadros (rtcFps > 0), garante que o <video> esteja reproduzindo
      if (rtcFps > 0) {
        this.lastFrameProgressAt = Date.now();
        this.lastTimeUpdate = Date.now();
        if (!this.isVideoRendering()) {
          this.tryPlay();
        } else {
          this.updateStatus("ONLINE", "", "online");
        }
      }

      let fps = Math.max(rvfcFps, qualityFps, rtcFps);

      if (rtcFps === 0 && (!this.video || this.video.paused || !this.video.srcObject)) {
        fps = 0;
      }

      this.currentFps = fps;
      this.renderFpsBadge(fps);
    }, 1000);
  }

  renderFpsBadge(fps) {
    if (!this.fpsBadge) return;
    if (this.fpsValEl) {
      this.fpsValEl.textContent = `${fps} FPS`;
    } else {
      this.fpsBadge.textContent = `${fps} FPS`;
    }

    if (fps >= 1) {
      this.fpsBadge.classList.add("fps-active");
      this.fpsBadge.classList.remove("fps-zero");
    } else {
      this.fpsBadge.classList.add("fps-zero");
      this.fpsBadge.classList.remove("fps-active");
    }
  }

  /**
   * Watchdog contínuo: garante que o vídeo nunca fique pausado na TV,
   * mantém a reprodução colada no tempo real (live-edge) após oscilações de RF 2.4GHz
   * e reconecta rapidamente se o decodificador parar de receber quadros por > 8s.
   */
  startWatchdog() {
    if (this.watchdogTimer) clearInterval(this.watchdogTimer);
    this.watchdogTimer = setInterval(() => {
      if (this.video && this.video.srcObject && !this.isVideoRendering() && !document.hidden) {
        this.tryPlay();
      }

      // Monitora avanço real de quadros decodificados na GPU da TV
      const decoded = this.getDecodedFrameCount();
      if (decoded > 0 && decoded !== this.lastDecodedFrames) {
        this.lastDecodedFrames = decoded;
        this.lastFrameProgressAt = Date.now();
        this.lastTimeUpdate = Date.now();
        this.updateStatus("ONLINE", "", "online");
      }

      // Verifica se a conexão ficou congelada sem decodificar novos frames por mais de 8s
      const isPcConnected =
        this.pc &&
        (this.pc.connectionState === "connected" ||
          this.pc.iceConnectionState === "connected" ||
          this.pc.iceConnectionState === "completed");

      if (isPcConnected && this.connectedAt > 0 && Date.now() - this.connectedAt > 8000) {
        const now = Date.now();
        const noFramesYet = !this.isVideoRendering() && this.prevRtcDecodedFrames === 0 && now - this.connectedAt > 8500;
        const stalledByFrames = this.lastFrameProgressAt > 0 && now - this.lastFrameProgressAt > 8000;
        const stalledByTime = this.lastFrameProgressAt === 0 && this.lastTimeUpdate > 0 && now - this.lastTimeUpdate > 9000;

        if (noFramesYet || stalledByFrames || stalledByTime) {
          this.connectedAt = now;
          this.lastFrameProgressAt = now;
          this.lastTimeUpdate = now;
          this.connect();
        }
      }
    }, 1200);
  }

  async connect() {
    if (this.isConnecting) return;
    this.isConnecting = true;
    this.playPendingUntil = 0;
    this.stopPlayRetryLoop();
    this.lastDecodedFrames = -1;
    this.prevTotalFrames = 0;
    this.prevRtcDecodedFrames = 0;
    this.lastFrameProgressAt = 0;

    this.updateStatus("CONECTANDO", "Negociando WHEP...", "connecting");

    if (this.pc) {
      try { this.pc.close(); } catch (e) {}
      this.pc = null;
    }

    try {
      // Operação 100% LAN-First (iceServers vazio): funciona instantaneamente mesmo com a Internet (WAN) desconectada
      this.pc = new RTCPeerConnection({
        iceServers: []
      });

      this.pc.addTransceiver("video", { direction: "recvonly" });

      this.pc.ontrack = (event) => {
        if (event.track && event.track.kind !== "video") {
          try {
            event.track.enabled = false;
            event.track.stop();
          } catch (e) {}
          return;
        }

        // Remove qualquer faixa de áudio que eventualmente exista na stream
        if (event.streams && event.streams[0]) {
          event.streams[0].getAudioTracks().forEach((audioTrack) => {
            try {
              audioTrack.enabled = false;
              audioTrack.stop();
              event.streams[0].removeTrack(audioTrack);
            } catch (e) {}
          });
        }

        // Usa a stream nativa do WebRTC (já sem áudio) ou cria MediaStream dedicada de vídeo
        const inboundStream =
          event.streams && event.streams[0] ? event.streams[0] : new MediaStream([event.track]);
        this.inboundTrack = event.track;
        this.inboundStream = inboundStream;
        this.enforceMutedState();
        this.video.srcObject = inboundStream;

        // Não chama video.play() sincronicamente aqui (readyState === 0):
        // aguarda o primeiro frame (loadedmetadata / loadeddata / canplay) para preservar o autoplay nativo do Safari e Firefox
        this.startPlayRetryLoop();
      };

      const handleConnState = (state) => {
        if (state === "connected" || state === "completed") {
          this.reconnectDelay = 1200;
          if (this.connectedAt === 0) this.connectedAt = Date.now();
          this.lastFrameProgressAt = Date.now();
          if (this.video && this.video.readyState >= 1) {
            this.tryPlay();
          }
        } else if (state === "disconnected" || state === "failed" || state === "closed") {
          this.updateStatus("RECONECTANDO", "Sinal interrompido...", "offline");
          this.scheduleReconnect();
        }
      };

      this.pc.onconnectionstatechange = () => {
        if (!this.pc) return;
        handleConnState(this.pc.connectionState);
      };

      this.pc.oniceconnectionstatechange = () => {
        if (!this.pc) return;
        handleConnState(this.pc.iceConnectionState);
      };

      const offer = await this.pc.createOffer();
      await this.pc.setLocalDescription(offer);

      // Aguarda coleta de candidatos ICE locais (essencial para Firefox Kiosk enviar a=candidate no SDP Offer)
      if (this.pc.iceGatheringState !== "complete") {
        await new Promise((resolve) => {
          const checkGathering = () => {
            if (!this.pc || this.pc.iceGatheringState === "complete") {
              cleanup();
              resolve();
            }
          };
          const gatherTimer = setTimeout(() => {
            cleanup();
            resolve();
          }, 250);
          const cleanup = () => {
            clearTimeout(gatherTimer);
            if (this.pc) {
              this.pc.removeEventListener("icegatheringstatechange", checkGathering);
            }
          };
          this.pc.addEventListener("icegatheringstatechange", checkGathering);
        });
      }

      const finalOfferSDP =
        this.pc && this.pc.localDescription && this.pc.localDescription.sdp
          ? this.pc.localDescription.sdp
          : offer.sdp;

      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 4000);

      const whepUrl = `/api/whep/${this.cameraId}?stream=${this.streamProfile || "sub"}`;
      const resp = await fetch(whepUrl, {
        method: "POST",
        headers: { "Content-Type": "application/sdp" },
        body: finalOfferSDP,
        signal: controller.signal,
      });
      clearTimeout(timeoutId);

      if (!resp.ok) {
        throw new Error(`Servidor WHEP retornou status HTTP ${resp.status}`);
      }

      const answerSDP = await resp.text();
      await this.pc.setRemoteDescription({
        type: "answer",
        sdp: answerSDP,
      });

      this.isConnecting = false;
    } catch (err) {
      this.isConnecting = false;
      this.updateStatus("SEM SINAL", "Reconectando...", "offline");
      this.scheduleReconnect();
    }
  }

  /**
   * Altera dinamicamente o perfil de transmissão (substream para grade ou mainstream para tela cheia)
   */
  setProfile(newProfile) {
    if (this.streamProfile === newProfile) return;
    this.streamProfile = newProfile;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.isConnecting = false;
    this.connect();
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
    // Adiciona jitter aleatório (0 a 500ms) para evitar que as 3 câmeras saturem o Wi-Fi 2.4GHz no mesmo instante
    const jitter = Math.floor(Math.random() * 500);
    const delay = this.reconnectDelay + jitter;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.reconnectDelay = Math.min(Math.round(this.reconnectDelay * 1.35), this.maxReconnectDelay);
      this.connect();
    }, delay);
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
    const fpsBadge = document.getElementById(`fps-${camId}`);

    if (video) {
      window.argusPlayers[camId] = new WHEPPlayer(camId, video, badge, overlay, statusText, fpsBadge);
    }
  });

  // Retoma reprodução automática de todas as câmeras ao voltar do standby da TV ou qualquer tecla do controle remoto
  const resumeAllCameras = (fromGesture = false) => {
    if (!window.argusPlayers) return;
    Object.values(window.argusPlayers).forEach((player) => {
      player.playPendingUntil = 0;
      player.tryPlay(fromGesture === true);
    });
  };

  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) {
      resumeAllCameras(false);
      requestTVWakeLock();
    }
  });
  window.addEventListener("focus", () => resumeAllCameras(false));
  window.addEventListener("pageshow", () => resumeAllCameras(false));
  document.addEventListener("keydown", () => resumeAllCameras(true));
  document.addEventListener("click", () => resumeAllCameras(true));
  document.addEventListener("touchstart", () => resumeAllCameras(true), { passive: true });

  // Reconexão imediata quando a interface Wi-Fi da TV recuperar sinal após queda ou interferência de RF
  window.addEventListener("online", () => {
    if (!window.argusPlayers) return;
    Object.values(window.argusPlayers).forEach((player) => {
      player.reconnectDelay = 800;
      if (!player.pc || player.pc.connectionState !== "connected" || player.video.readyState < 2) {
        player.connect();
      } else {
        player.tryPlay();
      }
    });
  });

  // Mantém a tela da Smart TV sempre ativa (Screen Wake Lock API)
  let wakeLockSentinel = null;
  const requestTVWakeLock = async () => {
    if ("wakeLock" in navigator && !document.hidden) {
      try {
        wakeLockSentinel = await navigator.wakeLock.request("screen");
      } catch (e) {}
    }
  };
  requestTVWakeLock();

  // Controle de Tela Cheia do Navegador (botão dedicado)
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

  // Gerenciador de Tela Cheia Individual por Câmera (Substream na grade ➔ Mainstream em tela cheia)
  let activeFullscreenCard = null;

  const exitCameraFullscreen = () => {
    if (!activeFullscreenCard) return;
    const card = activeFullscreenCard;
    const camId = card.getAttribute("data-camera-id");
    card.classList.remove("camera-fullscreen-active");

    const closeBtn = card.querySelector(".cam-fullscreen-close-btn");
    if (closeBtn) closeBtn.remove();

    activeFullscreenCard = null;

    // Retorna a câmera para o perfil de SUBSTREAM leve na grade 2x2
    if (camId && window.argusPlayers && window.argusPlayers[camId]) {
      window.argusPlayers[camId].setProfile("sub");
    }
  };

  const enterCameraFullscreen = (card) => {
    if (activeFullscreenCard === card) {
      exitCameraFullscreen();
      return;
    }
    if (activeFullscreenCard) {
      exitCameraFullscreen();
    }

    const camId = card.getAttribute("data-camera-id");
    card.classList.add("camera-fullscreen-active");
    activeFullscreenCard = card;

    // Cria botão discreto para fechar tela cheia
    let closeBtn = card.querySelector(".cam-fullscreen-close-btn");
    if (!closeBtn) {
      closeBtn = document.createElement("button");
      closeBtn.className = "cam-fullscreen-close-btn";
      closeBtn.setAttribute("type", "button");
      closeBtn.innerHTML = `✕ FECHAR (ESC)`;
      closeBtn.addEventListener("click", (evt) => {
        evt.stopPropagation();
        exitCameraFullscreen();
      });
      card.appendChild(closeBtn);
    }

    // Chaveia o player desta câmera imediatamente para o MAINSTREAM em alta resolução
    if (camId && window.argusPlayers && window.argusPlayers[camId]) {
      window.argusPlayers[camId].setProfile("main");
    }
  };

  // Clique duplo sobre uma câmera maximiza ou restaura
  cards.forEach((card) => {
    card.setAttribute("tabindex", "0");
    card.addEventListener("dblclick", (e) => {
      e.stopPropagation();
      enterCameraFullscreen(card);
    });
  });

  // Duplo clique fora das câmeras (ex: no HUD) alterna fullscreen geral da janela do navegador
  document.addEventListener("dblclick", (e) => {
    if (!e.target.closest(".camera-card") && e.target.tagName !== "BUTTON") {
      toggleFullscreen();
    }
  });

  // Atalhos de teclado e controle remoto (Escape / Back fecha a câmera individual)
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" || e.key === "Backspace" || e.key === "BrowserBack") {
      if (activeFullscreenCard) {
        e.preventDefault();
        exitCameraFullscreen();
      }
    } else if (e.key === "Enter" || e.key === " ") {
      // Se um card de câmera estiver com foco (D-Pad do controle remoto)
      const focusedCard = document.activeElement ? document.activeElement.closest(".camera-card") : null;
      if (focusedCard) {
        e.preventDefault();
        enterCameraFullscreen(focusedCard);
      }
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
