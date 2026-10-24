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
	id          string
	name        string
	rtspURL     string
	trackH264   *webrtc.TrackLocalStaticRTP
	trackH265   *webrtc.TrackLocalStaticRTP
	sessions    map[string]*webrtc.PeerConnection
	sessMu      sync.RWMutex
	connected   bool
	cancel      context.CancelFunc
	packetCount uint64
	lastPacket  time.Time
	lastError   string
	codecInfo   string
	isH265      bool
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
		result = append(result, map[string]interface{}{
			"id":               cam.id,
			"name":             cam.name,
			"connected":        cam.connected,
			"viewers":          sessCount,
			"packets_received": pktCount,
			"last_packet":      cam.lastPacket.Format(time.RFC3339),
			"last_error":       cam.lastError,
			"codec":            cam.codecInfo,
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
			cam.lastError = fmt.Sprintf("URL inválida: %v", err)
			log.Printf("[RTSP] URL inválida para %s (%s): %v", cam.id, cam.name, err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Utiliza RTSP over TCP para máxima estabilidade e evitar perdas de pacotes UDP na rede
		transport := gortsplib.TransportTCP
		client := &gortsplib.Client{
			Transport: &transport,
		}

		err = client.Start(u.Scheme, u.Host)
		if err != nil {
			cam.lastError = fmt.Sprintf("Erro ao conectar no host %s: %v", u.Host, err)
			log.Printf("[RTSP] Erro ao conectar em %s (%s - %s): %v", cam.id, cam.name, u.Host, err)
			time.Sleep(3 * time.Second)
			continue
		}

		desc, _, err := client.Describe(u)
		if err != nil {
			cam.lastError = fmt.Sprintf("Falha no DESCRIBE (credenciais/autenticação): %v", err)
			log.Printf("[RTSP] Falha no DESCRIBE de %s: %v", cam.id, err)
			client.Close()
			time.Sleep(3 * time.Second)
			continue
		}

		// Localiza track de vídeo H.265 ou H.264
		var formaH264 *format.H264
		var formaH265 *format.H265
		mediH264 := desc.FindFormat(&formaH264)
		mediH265 := desc.FindFormat(&formaH265)

		wasH265 := cam.isH265
		if mediH265 != nil {
			cam.isH265 = true
			cam.codecInfo = fmt.Sprintf("H.265 (PT %d)", formaH265.PayloadType())
			log.Printf("[RTSP] ✓ Codec H.265 (HEVC) detectado para %s (%s)", cam.id, cam.name)
		} else if mediH264 != nil {
			cam.isH265 = false
			cam.codecInfo = fmt.Sprintf("H.264 (PT %d)", formaH264.PayloadType())
			log.Printf("[RTSP] ✓ Codec H.264 detectado para %s (%s)", cam.id, cam.name)
		} else {
			cam.lastError = "Nenhum codec de vídeo H.264 ou H.265 encontrado no SDP da câmera"
			log.Printf("[RTSP] ❌ Nenhum formato H.264/H.265 para %s", cam.id)
			client.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		// Se o codec detectado mudou (ex: cliente conectou antes do DESCRIBE em câmera H.265),
		// encerra sessões antigas para forçar renegociação imediata na track correta
		if wasH265 != cam.isH265 {
			cam.sessMu.Lock()
			for id, pc := range cam.sessions {
				_ = pc.Close()
				delete(cam.sessions, id)
			}
			cam.sessMu.Unlock()
		}

		err = client.SetupAll(desc.BaseURL, desc.Medias)
		if err != nil {
			cam.lastError = fmt.Sprintf("Falha no SETUP: %v", err)
			log.Printf("[RTSP] Falha no SETUP de %s: %v", cam.id, err)
			client.Close()
			time.Sleep(3 * time.Second)
			continue
		}

		// Repasse direto zero-copy de RTP para o WebRTC Pion
		client.OnPacketRTPAny(func(m *description.Media, f format.Format, pkt *rtp.Packet) {
			cam.connected = true
			atomic.AddUint64(&cam.packetCount, 1)
			cam.lastPacket = time.Now()
			cam.lastError = ""
			p := *pkt

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
			cam.lastError = fmt.Sprintf("Falha no PLAY: %v", err)
			log.Printf("[RTSP] Falha no PLAY de %s: %v", cam.id, err)
			client.Close()
			time.Sleep(3 * time.Second)
			continue
		}

		log.Printf("[RTSP] ✓ Stream conectado e ativo: %s (%s) [%s]", cam.id, cam.name, cam.codecInfo)

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
			log.Printf("[RTSP] Conexão com %s interrompida: %v. Reconectando em 2s...", cam.id, err)
		}

		cam.connected = false
		client.Close()
		time.Sleep(2 * time.Second)
	}
}
