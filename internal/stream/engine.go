package stream

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"argus/internal/config"
	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtp"
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
			log.Printf("[RTSP] Tentando conectar em %s (%s)...", cam.id, cam.name)
			time.Sleep(3 * time.Second)
			continue
		}

		desc, _, err := client.Describe(u)
		if err != nil {
			log.Printf("[RTSP] Falha no DESCRIBE de %s: %v", cam.id, err)
			client.Close()
			time.Sleep(3 * time.Second)
			continue
		}

		// Localiza a track de vídeo com codec H.264
		var forma *format.H264
		medi := desc.FindFormat(&forma)
		if medi == nil {
			log.Printf("[RTSP] Nenhuma track H.264 encontrada para %s", cam.id)
			client.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		_, err = client.Setup(desc.BaseURL, medi, 0, 0)
		if err != nil {
			log.Printf("[RTSP] Falha no SETUP de %s: %v", cam.id, err)
			client.Close()
			time.Sleep(3 * time.Second)
			continue
		}

		// Repasse direto zero-copy de RTP para o WebRTC Pion
		client.OnPacketRTP(medi, forma, func(pkt *rtp.Packet) {
			cam.connected = true
			p := *pkt
			p.Header.PayloadType = 96
			_ = cam.track.WriteRTP(&p)
		})

		_, err = client.Play(nil)
		if err != nil {
			log.Printf("[RTSP] Falha no PLAY de %s: %v", cam.id, err)
			client.Close()
			time.Sleep(3 * time.Second)
			continue
		}

		log.Printf("[RTSP] ✓ Stream H.264 conectado e ativo: %s (%s)", cam.id, cam.name)

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
