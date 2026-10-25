package stream

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"argus/internal/config"
	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type cameraStream struct {
	id             string
	name           string
	rtspURL        string
	trackH264      *webrtc.TrackLocalStaticRTP
	trackH265      *webrtc.TrackLocalStaticRTP
	sessions       map[string]*webrtc.PeerConnection
	sessMu         sync.RWMutex
	connected      atomic.Bool
	isH265         atomic.Bool
	cancel         context.CancelFunc
	packetCount    uint64
	lastPacketNano atomic.Int64
	seqNumber      uint32
	stateMu        sync.RWMutex
	lastError      string
	codecInfo      string
}

func (cs *cameraStream) setError(errStr string) {
	cs.stateMu.Lock()
	cs.lastError = errStr
	cs.stateMu.Unlock()
}

func (cs *cameraStream) setCodecInfo(info string) {
	cs.stateMu.Lock()
	cs.codecInfo = info
	cs.stateMu.Unlock()
}

func (cs *cameraStream) getState() (lastErr string, codec string) {
	cs.stateMu.RLock()
	lastErr = cs.lastError
	codec = cs.codecInfo
	cs.stateMu.RUnlock()
	return
}

func (cs *cameraStream) closeAllSessions() {
	cs.sessMu.Lock()
	for id, pc := range cs.sessions {
		_ = pc.Close()
		delete(cs.sessions, id)
	}
	cs.sessMu.Unlock()
}

// Engine orquestra os streams de vídeo e as sessões WebRTC WHEP
type Engine struct {
	cameras     map[string]*cameraStream
	camerasMu   sync.RWMutex
	api         *webrtc.API
	mediaEngine *webrtc.MediaEngine
}

// NewEngine inicializa o motor de streaming registrando codecs e tracks
func NewEngine(cameras []config.CameraConfig) *Engine {
	m := &webrtc.MediaEngine{}

	// Registra suporte a H.264
	_ = m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH264,
			ClockRate:    90000,
			Channels:     0,
			SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
			RTCPFeedback: nil,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo)

	// Registra suporte a H.265 (HEVC)
	_ = m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH265,
			ClockRate:    90000,
			Channels:     0,
			SDPFmtpLine:  "",
			RTCPFeedback: nil,
		},
		PayloadType: 98,
	}, webrtc.RTPCodecTypeVideo)

	if err := m.RegisterDefaultCodecs(); err != nil {
		log.Printf("[Stream] Aviso ao registrar codecs padrão: %v", err)
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))

	engine := &Engine{
		cameras:     make(map[string]*cameraStream),
		api:         api,
		mediaEngine: m,
	}

	for _, cam := range cameras {
		trackH264, err := webrtc.NewTrackLocalStaticRTP(
			webrtc.RTPCodecCapability{
				MimeType:  webrtc.MimeTypeH264,
				ClockRate: 90000,
			},
			"video",
			"stream-"+cam.ID+"-h264",
		)
		if err != nil {
			log.Printf("[Stream] Erro ao criar track H.264 para %s: %v", cam.ID, err)
			continue
		}

		trackH265, err := webrtc.NewTrackLocalStaticRTP(
			webrtc.RTPCodecCapability{
				MimeType:  webrtc.MimeTypeH265,
				ClockRate: 90000,
			},
			"video",
			"stream-"+cam.ID+"-h265",
		)
		if err != nil {
			log.Printf("[Stream] Erro ao criar track H.265 para %s: %v", cam.ID, err)
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		cs := &cameraStream{
			id:        cam.ID,
			name:      cam.Name,
			rtspURL:   cam.RTSPURL,
			trackH264: trackH264,
			trackH265: trackH265,
			sessions:  make(map[string]*webrtc.PeerConnection),
			cancel:    cancel,
		}

		engine.cameras[cam.ID] = cs

		// Inicia rotina de ingestão RTSP ativa com zero-copy passthrough
		go engine.startRTSPIngest(ctx, cs)
	}

	return engine
}

// GetCamera retorna o stream de uma câmera pelo ID
func (e *Engine) GetCamera(id string) (*cameraStream, bool) {
	e.camerasMu.RLock()
	defer e.camerasMu.RUnlock()
	cs, exists := e.cameras[id]
	return cs, exists
}

// ListCameras retorna a lista de IDs e nomes das câmeras registradas
func maskURL(raw string) string {
	if idx := strings.Index(raw, "@"); idx != -1 {
		prefix := raw[:idx]
		if colon := strings.Index(prefix, "://"); colon != -1 {
			scheme := prefix[:colon+3]
			return scheme + "***:***@" + raw[idx+1:]
		}
	}
	return raw
}

// ListCameras retorna a lista de IDs e nomes das câmeras registradas com status de streaming
func (e *Engine) ListCameras() []map[string]interface{} {
	e.camerasMu.RLock()
	defer e.camerasMu.RUnlock()

	result := make([]map[string]interface{}, 0, len(e.cameras))
	for _, cam := range e.cameras {
		cam.sessMu.RLock()
		sessCount := len(cam.sessions)
		cam.sessMu.RUnlock()

		pktCount := atomic.LoadUint64(&cam.packetCount)
		lastNano := cam.lastPacketNano.Load()
		var lastPacketStr string
		if lastNano > 0 {
			lastPacketStr = time.Unix(0, lastNano).Format(time.RFC3339)
		}
		lastErr, codec := cam.getState()

		result = append(result, map[string]interface{}{
			"id":               cam.id,
			"name":             cam.name,
			"connected":        cam.connected.Load(),
			"viewers":          sessCount,
			"packets_received": pktCount,
			"last_packet":      lastPacketStr,
			"last_error":       lastErr,
			"codec":            codec,
			"url":              maskURL(cam.rtspURL),
		})
	}
	return result
}

// Close encerra todas as conexões WebRTC e cancela workers de RTSP
func (e *Engine) Close() {
	e.camerasMu.Lock()
	defer e.camerasMu.Unlock()

	for _, cam := range e.cameras {
		if cam.cancel != nil {
			cam.cancel()
		}
		cam.sessMu.Lock()
		for id, pc := range cam.sessions {
			_ = pc.Close()
			delete(cam.sessions, id)
		}
		cam.sessMu.Unlock()
	}
}

// startRTSPIngest gerencia a conexão RTSP nativa e repassa pacotes RTP H.264 diretamente para o WebRTC
func (e *Engine) startRTSPIngest(ctx context.Context, cam *cameraStream) {
	rawURL := strings.TrimSpace(cam.rtspURL)
	rawURL = strings.Trim(rawURL, `"'`)
	if idx := strings.Index(rawURL, "#"); idx != -1 {
		rawURL = rawURL[:idx]
	}

	if rawURL == "" || strings.HasPrefix(rawURL, "${") {
		log.Printf("[RTSP] ⚠️ Câmera %s (%s) está sem URL RTSP configurada no .env", cam.id, cam.name)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		u, err := base.ParseURL(rawURL)
		if err != nil {
			cam.setError(fmt.Sprintf("URL inválida: %v", err))
			log.Printf("[RTSP] URL inválida para %s (%s): %v", cam.id, cam.name, err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Utiliza RTSP over TCP com timeouts curtos (4s) para detectar rapidamente
		// congelamentos por interferência de RF em 2.4GHz e reconectar imediatamente quando o sinal voltar
		transport := gortsplib.TransportTCP
		client := &gortsplib.Client{
			Transport:    &transport,
			ReadTimeout:  4 * time.Second,
			WriteTimeout: 4 * time.Second,
		}

		err = client.Start(u.Scheme, u.Host)
		if err != nil {
			cam.setError(fmt.Sprintf("Erro ao conectar no host %s: %v", u.Host, err))
			log.Printf("[RTSP] Erro ao conectar em %s (%s - %s): %v", cam.id, cam.name, u.Host, err)
			time.Sleep(2 * time.Second)
			continue
		}

		desc, _, err := client.Describe(u)
		if err != nil {
			cam.setError(fmt.Sprintf("Falha no DESCRIBE (credenciais/autenticação): %v", err))
			log.Printf("[RTSP] Falha no DESCRIBE de %s: %v", cam.id, err)
			client.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		// Localiza track de vídeo H.265 ou H.264
		var formaH264 *format.H264
		var formaH265 *format.H265
		mediH264 := desc.FindFormat(&formaH264)
		mediH265 := desc.FindFormat(&formaH265)

		wasH265 := cam.isH265.Load()
		var codecName string
		if mediH265 != nil {
			cam.isH265.Store(true)
			codecName = fmt.Sprintf("H.265 (PT %d)", formaH265.PayloadType())
			cam.setCodecInfo(codecName)
			log.Printf("[RTSP] ✓ Codec H.265 (HEVC) detectado para %s (%s)", cam.id, cam.name)
		} else if mediH264 != nil {
			cam.isH265.Store(false)
			codecName = fmt.Sprintf("H.264 (PT %d)", formaH264.PayloadType())
			cam.setCodecInfo(codecName)
			log.Printf("[RTSP] ✓ Codec H.264 detectado para %s (%s)", cam.id, cam.name)
		} else {
			cam.setError("Nenhum codec de vídeo H.264 ou H.265 encontrado no SDP da câmera")
			log.Printf("[RTSP] ❌ Nenhum formato H.264/H.265 para %s", cam.id)
			client.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		// Se o codec detectado mudou ou a câmera reconectou após queda de RF,
		// encerra sessões WebRTC antigas para forçar renegociação limpa na TV
		if wasH265 != cam.isH265.Load() {
			cam.closeAllSessions()
		}

		err = client.SetupAll(desc.BaseURL, desc.Medias)
		if err != nil {
			cam.setError(fmt.Sprintf("Falha no SETUP: %v", err))
			log.Printf("[RTSP] Falha no SETUP de %s: %v", cam.id, err)
			client.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		// Repasse direto zero-copy e lock-free de RTP para o WebRTC Pion
		// Reescreve SequenceNumber de forma monotônica para evitar congelamento do SRTP/Jitter Buffer após micro-cortes de RF
		client.OnPacketRTPAny(func(m *description.Media, f format.Format, pkt *rtp.Packet) {
			if !cam.connected.Load() {
				cam.connected.Store(true)
				cam.setError("")
			}
			cnt := atomic.AddUint64(&cam.packetCount, 1)
			if cnt == 1 || cnt%30 == 0 {
				cam.lastPacketNano.Store(time.Now().UnixNano())
			}

			p := *pkt
			seq := uint16(atomic.AddUint32(&cam.seqNumber, 1))
			p.Header.SequenceNumber = seq

			if _, ok := f.(*format.H265); ok {
				p.Header.PayloadType = 98
				_ = cam.trackH265.WriteRTP(&p)
			} else if _, ok := f.(*format.H264); ok {
				p.Header.PayloadType = 96
				_ = cam.trackH264.WriteRTP(&p)
			}
		})

		_, err = client.Play(nil)
		if err != nil {
			cam.setError(fmt.Sprintf("Falha no PLAY: %v", err))
			log.Printf("[RTSP] Falha no PLAY de %s: %v", cam.id, err)
			client.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		log.Printf("[RTSP] ✓ Stream conectado e ativo: %s (%s) [%s]", cam.id, cam.name, codecName)

		// Aguarda enquanto a conexão estiver ativa ou contexto encerrado
		waitErr := make(chan error, 1)
		go func() {
			waitErr <- client.Wait()
		}()

		select {
		case <-ctx.Done():
			client.Close()
			return
		case err := <-waitErr:
			cam.setError(fmt.Sprintf("Sinal interrompido: %v", err))
			log.Printf("[RTSP] Conexão com %s interrompida (possível interferência RF/Wi-Fi): %v. Reconectando em 1.5s...", cam.id, err)
		}

		cam.connected.Store(false)
		client.Close()
		// Encerra sessões WebRTC ativas desta câmera para que a TV saiba imediatamente
		// que o sinal caiu e renegocie uma sessão limpa assim que o RTSP voltar
		cam.closeAllSessions()
		time.Sleep(1500 * time.Millisecond)
	}
}
