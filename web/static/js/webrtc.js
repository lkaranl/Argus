/**
 * MODELO MATEMÁTICO DE ESTABILIZAÇÃO DE FLUIDEZ (Jacobson-Karels Adaptativo)
 * 
 * Problema: Redes Wi-Fi residenciais (especialmente 2.4 GHz em câmeras externas)
 * sofrem com flutuações de tempo de trânsito dos pacotes (jitter) e retransmissões NACK.
 * O WebRTC padrão tenta operar com jitter buffer tendendo a 0ms, causando "underrun"
 * e micro-congelamentos visuais toda vez que um pacote atrasa 40ms.
 * 
 * Solução Matemática:
 * 1. Estimador de Jitter Médio Suavizado (Filtro Exponencial Jacobson-Karels):
 *    J_smooth(t) = (1 - α) * J_smooth(t-1) + α * J_sample(t),  onde α = 0.125 (1/8)
 * 2. Estimador de Dispersão / Variância Absoluta Média (MAD):
 *    J_dev(t) = (1 - β) * J_dev(t-1) + β * |J_sample(t) - J_smooth(t)|,  onde β = 0.25 (1/4)
 * 3. Dimensionamento Ótimo do Buffer de Playout:
 *    Target(t) = BaseDelay + J_smooth(t) + K * J_dev(t)
 *    onde K = 4.0 (garante cobertura de 99.7% das flutuações de cauda),
 *    e BaseDelay = 0.10s (tempo de 1 frame a 10 FPS).
 * 4. Histerese Assimétrica com Zona Morta (Deadband):
 *    - Se Target > BufferAtual + 25ms: Subida imediata (Fast Attack) para blindar o decodificador.
 *    - Se Target < BufferAtual - 40ms: Descida amortecida (Slow Decay, 0.85/0.15) após 3.5s de calma.
 *    - Diferenças < 25ms são ignoradas para evitar que o relógio de reprodução sofra micro-pitching.
 * 5. Injeção Bi-direcional na API de Renderização:
 *    - RTCRtpReceiver.playoutDelayHint (segundos)
 *    - RTCRtpReceiver.jitterBufferTarget (milissegundos)
 */
class AdaptiveJitterController {
  constructor(receiverProvider) {
    this.receiverProvider = receiverProvider;
    this.alpha = 0.125;
    this.beta = 0.25;
    this.kMultiplier = 4.0;
    this.baseDelaySec = 0.10;
    this.minDelaySec = 0.15; // Mínimo de 150ms
    this.maxDelaySec = 0.40; // Máximo de 400ms (imperceptível ao olho)

    this.smoothedJitter = 0.02;
    this.jitterDev = 0.01;
    this.appliedDelaySec = 0.20; // 200ms inicial padrão
    this.lastAdjustTime = 0;
  }

  reset() {
    this.smoothedJitter = 0.02;
    this.jitterDev = 0.01;
    this.appliedDelaySec = 0.20;
    this.lastAdjustTime = performance.now();
  }

  applyInitial(receiver) {
    if (!receiver) return;
    this.apply(this.appliedDelaySec, receiver);
  }

  update(jitterSec, underrunDetected = false) {
    if (typeof jitterSec !== "number" || isNaN(jitterSec) || jitterSec < 0) return;

    // Atualização matemática Jacobson-Karels
    const err = Math.abs(jitterSec - this.smoothedJitter);
    this.smoothedJitter = (1 - this.alpha) * this.smoothedJitter + this.alpha * jitterSec;
    this.jitterDev = (1 - this.beta) * this.jitterDev + this.beta * err;

    let target = this.baseDelaySec + this.smoothedJitter + (this.kMultiplier * this.jitterDev);

    if (underrunDetected) {
      target += 0.06; // Incremento defensivo de +60ms se houve perda de pacote ou frame drop
    }

    const clampedTarget = Math.min(Math.max(target, this.minDelaySec), this.maxDelaySec);
    const diff = clampedTarget - this.appliedDelaySec;
    const now = performance.now();

    if (diff > 0.025) {
      // Fast Attack: eleva o buffer preventivamente
      this.appliedDelaySec = clampedTarget;
      this.lastAdjustTime = now;
      this.apply(this.appliedDelaySec);
    } else if (diff < -0.040 && (now - this.lastAdjustTime > 3500)) {
      // Slow Decay: reduz gradualmente
      this.appliedDelaySec = this.appliedDelaySec * 0.85 + clampedTarget * 0.15;
      this.lastAdjustTime = now;
      this.apply(this.appliedDelaySec);
    }
  }

  apply(sec, receiverOverride = null) {
    const receiver = receiverOverride || (typeof this.receiverProvider === "function" ? this.receiverProvider() : this.receiverProvider);
    if (!receiver) return;

    const roundedSec = Math.round(sec * 1000) / 1000;
    const roundedMs = Math.round(sec * 1000);

    try {
      if ("playoutDelayHint" in receiver) {
        receiver.playoutDelayHint = roundedSec;
      }
    } catch (e) {}

    try {
      if ("jitterBufferTarget" in receiver) {
        receiver.jitterBufferTarget = roundedMs;
      }
    } catch (e) {}
  }
}

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
    this.receiver = null;
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

    // Métricas para feedback defensivo do jitter buffer
    this.prevFramesDropped = 0;
    this.prevPacketsLost = 0;
    this.jitterController = new AdaptiveJitterController(() => this.receiver);

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
    if (!this.video || !this.video.srcObject) return false;
    return (
      this.video.currentTime > 0 ||
      this.getDecodedFrameCount() > 0 ||
      this.rvfcFrames > 0 ||
      this.currentFps > 0
    );
  }

  tryPlay(fromUserGesture = false) {
    if (!this.video || !this.video.srcObject) return;
    this.enforceMutedState();

    // Se o vídeo já está tocando, sincroniza status online
    if (!this.video.paused) {
      this.stopPlayRetryLoop();
      this.updateStatus("ONLINE", "", "online");
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
      if (!this.video.paused) {
        this.stopPlayRetryLoop();
        this.updateStatus("ONLINE", "", "online");
        return;
      }
      this.tryPlay();
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

              // Detecção de anomalias de entrega (frames descartados / pacotes perdidos)
              let underrunDetected = false;
              if (typeof report.framesDropped === "number") {
                if (this.prevFramesDropped !== undefined && report.framesDropped > this.prevFramesDropped) {
                  underrunDetected = true;
                }
                this.prevFramesDropped = report.framesDropped;
              }
              if (typeof report.packetsLost === "number") {
                if (this.prevPacketsLost !== undefined && report.packetsLost > this.prevPacketsLost) {
                  underrunDetected = true;
                }
                this.prevPacketsLost = report.packetsLost;
              }

              if (typeof report.jitter === "number") {
                this.jitterController.update(report.jitter, underrunDetected);
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

      // Determina o FPS bruto medido: prioriza a taxa nativa de quadros decodificados (rtcFps / qualityFps)
      let rawFps = 0;
      if (rtcFps > 0) {
        rawFps = rtcFps;
      } else if (qualityFps > 0) {
        rawFps = qualityFps;
      } else if (rvfcFps > 0) {
        rawFps = rvfcFps;
      }

      if (rawFps === 0 && (!this.video || this.video.paused || !this.video.srcObject)) {
        this.currentFps = 0;
      } else if (rawFps > 0) {
        // Suavização exponencial suave para manter o número estável no HUD
        this.currentFps = this.currentFps > 0 ? Math.round(this.currentFps * 0.3 + rawFps * 0.7) : rawFps;
      }

      this.renderFpsBadge(this.currentFps);
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
      this.updateStatus("ONLINE", "", "online");
    } else {
      this.fpsBadge.classList.add("fps-zero");
      this.fpsBadge.classList.remove("fps-active");
    }
  }

  /**
   * Watchdog contínuo: garante que o vídeo nunca fique pausado na TV,
   * mantém a reprodução colada no tempo real (live-edge) após oscilações de RF 2.4GHz
   * e reconecta se o decodificador parar de receber quadros por período prolongado.
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

      // Verifica se a conexão ficou congelada sem decodificar novos frames
      const isPcConnected =
        this.pc &&
        (this.pc.connectionState === "connected" ||
          this.pc.iceConnectionState === "connected" ||
          this.pc.iceConnectionState === "completed");

      if (isPcConnected && this.connectedAt > 0) {
        const now = Date.now();
        // Dá até 35s para o primeiro frame da transcodificação H.264 chegar e decodificar (ideal para clientes ARM como Raspberry Pi)
        const noFramesYet = !this.isVideoRendering() && this.prevRtcDecodedFrames === 0 && now - this.connectedAt > 35000;
        // Evita reconectar se o stream sofrer oscilações momentâneas de sinal na câmera Wi-Fi
        const stalledByFrames = this.lastFrameProgressAt > 0 && now - this.lastFrameProgressAt > 30000;
        const stalledByTime = this.lastFrameProgressAt === 0 && this.lastTimeUpdate > 0 && now - this.lastTimeUpdate > 30000;

        if (noFramesYet || stalledByFrames || stalledByTime) {
          this.connectedAt = now;
          this.lastFrameProgressAt = now;
          this.lastTimeUpdate = now;
          this.connect();
        }
      }
    }, 1500);
  }

  async connect() {
    if (this.isConnecting) return;
    this.isConnecting = true;
    this.playPendingUntil = 0;
    this.stopPlayRetryLoop();
    this.lastDecodedFrames = -1;
    this.prevTotalFrames = 0;
    this.prevRtcDecodedFrames = 0;
    this.prevFramesDropped = 0;
    this.prevPacketsLost = 0;
    this.lastFrameProgressAt = 0;
    this.jitterController.reset();

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
        this.receiver = event.receiver;

        // Inicializa o buffer adaptativo no RTCRtpReceiver nativo
        if (event.receiver) {
          this.jitterController.applyInitial(event.receiver);
        }

        this.enforceMutedState();
        this.video.srcObject = inboundStream;

        // Dispara reprodução imediata e mantém loop de retry
        this.tryPlay();
        this.startPlayRetryLoop();
      };

      const handleConnState = (state) => {
        if (state === "connected" || state === "completed") {
          this.reconnectDelay = 1200;
          if (this.connectedAt === 0) this.connectedAt = Date.now();
          this.lastFrameProgressAt = Date.now();
          if (this.video) {
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
      const timeoutId = setTimeout(() => controller.abort(), 15000);

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
