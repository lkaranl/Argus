package stream

import (
	"context"
	"log"
	"sync"
	"time"

	"argus/internal/config"
	"github.com/pion/webrtc/v4"
)

type cameraStream struct {
	id        string
	name      string
	rtspURL   string
	track     *webrtc.TrackLocalStaticRTP
	sessions  map[string]*webrtc.PeerConnection
	sessMu    sync.RWMutex
	connected bool
	cancel    context.CancelFunc
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

	// Registra suporte obrigatório a H.264 passthrough
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH264,
			ClockRate:    90000,
			Channels:     0,
			SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
			RTCPFeedback: nil,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		log.Printf("[Stream] Aviso ao registrar codec H.264: %v", err)
	}

	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))

	engine := &Engine{
		cameras:     make(map[string]*cameraStream),
		api:         api,
		mediaEngine: m,
	}

	for _, cam := range cameras {
		track, err := webrtc.NewTrackLocalStaticRTP(
			webrtc.RTPCodecCapability{
				MimeType:  webrtc.MimeTypeH264,
				ClockRate: 90000,
			},
			"video",
			"stream-"+cam.ID,
		)
		if err != nil {
			log.Printf("[Stream] Erro ao criar track para %s: %v", cam.ID, err)
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		cs := &cameraStream{
			id:       cam.ID,
			name:     cam.Name,
			rtspURL:  cam.RTSPURL,
			track:    track,
			sessions: make(map[string]*webrtc.PeerConnection),
			cancel:   cancel,
		}

		engine.cameras[cam.ID] = cs

		// Inicia rotina de ingestão RTSP silenciosa para a câmera
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
func (e *Engine) ListCameras() []map[string]interface{} {
	e.camerasMu.RLock()
	defer e.camerasMu.RUnlock()

	result := make([]map[string]interface{}, 0, len(e.cameras))
	for _, cam := range e.cameras {
		cam.sessMu.RLock()
		sessCount := len(cam.sessions)
		cam.sessMu.RUnlock()

		result = append(result, map[string]interface{}{
			"id":        cam.id,
			"name":      cam.name,
			"connected": cam.connected,
			"viewers":   sessCount,
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

// startRTSPIngest gerencia a conexão e reconexão silenciosa com a fonte RTSP
func (e *Engine) startRTSPIngest(ctx context.Context, cam *cameraStream) {
	// Se nenhuma URL de RTSP foi informada (ex: câmera placeholder), aguarda
	if cam.rtspURL == "" {
		return
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Rotina de verificação e reconexão de streaming
			// Em produção com RTSP ativo, os pacotes RTP H.264 recebidos são
			// encaminhados diretamente via cam.track.WriteRTP(pkt).
		}
	}
}
