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
    this.isPlayPending = false;
    this.connectedAt = 0;
    this.lastTimeUpdate = 0;
    this.lastCurrentTime = -1;
    this.lastDecodedFrames = -1;
    this.lastFrameProgressAt = 0;

    // Fallback de renderização via <canvas> (MediaStreamTrackProcessor / ImageCapture)
    // para navegadores/TVs que bloqueiam <video>.play() sem clique de mouse
    this.canvas = null;
    this.canvasCtx = null;
    this.canvasActive = false;
    this.canvasReader = null;
    this.canvasFrames = 0;

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

    // Cria elemento <canvas> de fallback dentro do container para renderização zero-click
    if (this.video.parentElement && !this.canvas) {
      this.canvas = document.createElement("canvas");
      this.canvas.className = "video-canvas";
      this.video.parentElement.insertBefore(this.canvas, this.video.nextSibling);
      this.canvasCtx = this.canvas.getContext("2d", { alpha: false, desynchronized: true });
    }

    const triggerPlay = () => {
      this.tryPlay();
    };

    this.video.addEventListener("loadedmetadata", triggerPlay);
    this.video.addEventListener("loadeddata", () => {
      this.tryPlay();
      if (!this.video.paused && this.video.readyState >= 2) {
        this.updateStatus("ONLINE", "", "online");
      }
    });
    this.video.addEventListener("canplay", triggerPlay);
    this.video.addEventListener("canplaythrough", triggerPlay);

    this.video.addEventListener("playing", () => {
      this.lastTimeUpdate = Date.now();
      this.lastFrameProgressAt = Date.now();
      if (this.video.readyState >= 2) {
        this.stopCanvasFallback();
        this.stopPlayRetryLoop();
        this.updateStatus("ONLINE", "", "online");
      }
    });

    this.video.addEventListener("timeupdate", () => {
      if (this.video.currentTime > 0 && this.video.currentTime !== this.lastCurrentTime) {
        this.lastCurrentTime = this.video.currentTime;
        this.lastTimeUpdate = Date.now();
        this.lastFrameProgressAt = Date.now();
        this.stopCanvasFallback();
        this.stopPlayRetryLoop();
        this.updateStatus("ONLINE", "", "online");
      }
    });

    // Se a TV ou o navegador tentar pausar o vídeo automaticamente, retoma na hora
    this.video.addEventListener("pause", () => {
      if (this.video.srcObject && !document.hidden) {
        setTimeout(() => this.tryPlay(), 100);
      }
    });

    this.video.addEventListener("suspend", () => {
      if (!this.isVideoRendering() && this.video.srcObject) {
        this.tryPlay();
      }
    });
  }

  enforceMutedState() {
    if (!this.video) return;
    if (!this.video.muted) this.video.muted = true;
    if (!this.video.defaultMuted) this.video.defaultMuted = true;
    if (this.video.volume !== 0) this.video.volume = 0;
  }

  isVideoRendering() {
    if (this.canvasActive && this.canvasFrames > 0) return true;
    if (!this.video || !this.video.srcObject || this.video.paused) return false;
    return (
      this.video.readyState >= 2 &&
      (this.getDecodedFrameCount() > 0 || this.video.currentTime > 0 || this.rvfcFrames > 0)
    );
  }

  tryPlay() {
    if (!this.video || !this.video.srcObject) return;
    this.enforceMutedState();

    // Se já está reproduzindo e decodificando quadros no <video>, apenas garante overlay oculto
    if (!this.video.paused && this.video.readyState >= 2 && (this.getDecodedFrameCount() > 0 || this.video.currentTime > 0)) {
      this.stopCanvasFallback();
      this.stopPlayRetryLoop();
      this.updateStatus("ONLINE", "", "online");
      return;
    }

    // Evita chamadas concorrentes a video.play() que geram AbortError no navegador
    if (this.isPlayPending) return;
    this.isPlayPending = true;

    try {
      const playPromise = this.video.play();
      if (playPromise !== undefined) {
        playPromise
          .then(() => {
            this.isPlayPending = false;
            if (!this.video.paused && this.video.readyState >= 2) {
              this.stopCanvasFallback();
              this.stopPlayRetryLoop();
              this.updateStatus("ONLINE", "", "online");
            } else {
              // play() resolveu antes do primeiro Keyframe chegar; mantém loop até readyState >= 2
              this.startPlayRetryLoop();
            }
          })
          .catch((err) => {
            this.isPlayPending = false;
            // Se o navegador bloquear <video>.play() por falta de clique (NotAllowedError),
            // ativa imediatamente a renderização direta via <canvas> sem precisar de mouse!
            if (err && err.name === "NotAllowedError") {
              this.startCanvasFallback();
            }
            this.startPlayRetryLoop();
          });
      } else {
        this.isPlayPending = false;
      }
    } catch (e) {
      this.isPlayPending = false;
      this.startPlayRetryLoop();
    }
  }

  /**
   * Renderizador direto de MediaStreamTrack para <canvas> (Zero-Click Fallback).
   * Ignora completamente o bloqueio de Autoplay do elemento <video> em clientes sem mouse.
   */
  async startCanvasFallback() {
    if (this.canvasActive || !this.inboundTrack || !this.canvas || !this.canvasCtx) return;
    if (this.inboundTrack.readyState === "ended") return;

    // 1. MediaStreamTrackProcessor (WebCodecs / Chromium / Edge / Brave / Android TV / WebOS)
    if (typeof window.MediaStreamTrackProcessor === "function") {
      try {
        this.canvasActive = true;
        this.canvas.classList.add("active");
        const processor = new window.MediaStreamTrackProcessor({ track: this.inboundTrack });
        const reader = processor.readable.getReader();
        this.canvasReader = reader;

        while (this.canvasActive) {
          const { value: frame, done } = await reader.read();
          if (done || !frame) break;

          const fw = frame.displayWidth || frame.codedWidth || 1280;
          const fh = frame.displayHeight || frame.codedHeight || 720;
          if (this.canvas.width !== fw || this.canvas.height !== fh) {
            this.canvas.width = fw;
            this.canvas.height = fh;
          }

          this.canvasCtx.drawImage(frame, 0, 0, fw, fh);
          frame.close();

          this.canvasFrames++;
          this.lastFrameProgressAt = Date.now();
          this.lastTimeUpdate = Date.now();
          this.updateStatus("ONLINE", "", "online");
        }
      } catch (e) {
      } finally {
        this.canvasActive = false;
      }
      return;
    }

    // 2. ImageCapture API fallback
    if (typeof window.ImageCapture === "function") {
      try {
        this.canvasActive = true;
        this.canvas.classList.add("active");
        const imageCapture = new window.ImageCapture(this.inboundTrack);

        const grabLoop = async () => {
          while (this.canvasActive && this.inboundTrack && this.inboundTrack.readyState === "live") {
            try {
              const bitmap = await imageCapture.grabFrame();
              if (this.canvas.width !== bitmap.width || this.canvas.height !== bitmap.height) {
                this.canvas.width = bitmap.width;
                this.canvas.height = bitmap.height;
              }
              this.canvasCtx.drawImage(bitmap, 0, 0);
              bitmap.close();
              this.canvasFrames++;
              this.lastFrameProgressAt = Date.now();
              this.lastTimeUpdate = Date.now();
              this.updateStatus("ONLINE", "", "online");
            } catch (err) {
              await new Promise((r) => setTimeout(r, 80));
            }
          }
          this.canvasActive = false;
        };
        grabLoop();
      } catch (e) {
        this.canvasActive = false;
      }
    }
  }

  stopCanvasFallback() {
    if (!this.canvasActive && (!this.canvas || !this.canvas.classList.contains("active"))) return;
    this.canvasActive = false;
    if (this.canvasReader) {
      try {
        this.canvasReader.cancel();
      } catch (e) {}
      this.canvasReader = null;
    }
    if (this.canvas) {
      this.canvas.classList.remove("active");
    }
  }

  startPlayRetryLoop() {
    if (this.playRetryTimer) return;
    this.playRetryTimer = setInterval(() => {
      if (!this.video || !this.video.srcObject) {
        this.stopPlayRetryLoop();
        return;
      }
      if (this.isVideoRendering() && !this.canvasActive) {
        this.stopPlayRetryLoop();
        this.updateStatus("ONLINE", "", "online");
        return;
      }
      this.tryPlay();
    }, 350);
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
      if (q && typeof q.totalVideoFrames === "number") {
        return q.totalVideoFrames;
      }
    }
    if (typeof this.video.webkitDecodedFrameCount === "number") {
      return this.video.webkitDecodedFrameCount;
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

      // 2. Contagem via fallback <canvas>
      const canvasFps = Math.round(this.canvasFrames / elapsedSec);
      this.canvasFrames = 0;

      // 3. Contagem via getVideoPlaybackQuality / webkitDecodedFrameCount
      let qualityFps = 0;
      const total = this.getDecodedFrameCount();
      if (total > 0) {
        if (this.prevTotalFrames > 0 && total >= this.prevTotalFrames) {
          qualityFps = Math.round((total - this.prevTotalFrames) / elapsedSec);
        }
        this.prevTotalFrames = total;
      }

      // 4. Contagem nativa do motor WebRTC C++ (inbound-rtp framesPerSecond / framesDecoded)
      let rtcFps = 0;
      if (this.pc) {
        try {
          const stats = await this.pc.getStats();
          stats.forEach((report) => {
            if (report.type === "inbound-rtp" && report.kind === "video") {
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

      // Se o motor WebRTC já está decodificando quadros (rtcFps > 0), mas o <video> ainda não iniciou,
      // força tryPlay() e aciona o fallback em <canvas> caso o <video> continue bloqueado
      if (rtcFps > 0) {
        this.lastFrameProgressAt = Date.now();
        this.lastTimeUpdate = Date.now();
        if (!this.isVideoRendering()) {
          this.tryPlay();
          if (this.video && this.video.paused) {
            this.startCanvasFallback();
          }
        } else {
          this.updateStatus("ONLINE", "", "online");
        }
      }

      let fps = Math.max(rvfcFps, qualityFps, rtcFps, canvasFps);

      if (!this.canvasActive && rtcFps === 0 && (!this.video || this.video.paused || !this.video.srcObject)) {
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
    this.isPlayPending = false;
    this.stopPlayRetryLoop();
    this.stopCanvasFallback();
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
        this.enforceMutedState();

        event.track.onunmute = () => {
          this.isPlayPending = false;
          this.tryPlay();
        };

        this.tryPlay();
        this.startPlayRetryLoop();
      };

      const handleConnState = (state) => {
        if (state === "connected" || state === "completed") {
          this.reconnectDelay = 1200;
          if (this.connectedAt === 0) this.connectedAt = Date.now();
          this.lastFrameProgressAt = Date.now();
          this.tryPlay();
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

      const offer = await this.pc.createOffer({
        offerToReceiveVideo: true,
        offerToReceiveAudio: false,
      });
      await this.pc.setLocalDescription(offer);

      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 4000);

      const resp = await fetch(`/api/whep/${this.cameraId}`, {
        method: "POST",
        headers: { "Content-Type": "application/sdp" },
        body: offer.sdp,
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
      this.tryPlay();
    } catch (err) {
      this.isConnecting = false;
      this.updateStatus("SEM SINAL", "Reconectando...", "offline");
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
  const resumeAllCameras = () => {
    if (!window.argusPlayers) return;
    Object.values(window.argusPlayers).forEach((player) => {
      player.tryPlay();
    });
  };

  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) {
      resumeAllCameras();
      requestTVWakeLock();
    }
  });
  window.addEventListener("focus", resumeAllCameras);
  window.addEventListener("pageshow", resumeAllCameras);
  document.addEventListener("keydown", resumeAllCameras);
  document.addEventListener("click", resumeAllCameras);
  document.addEventListener("touchstart", resumeAllCameras, { passive: true });

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
