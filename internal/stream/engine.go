package stream

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"argus/internal/config"
	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// rtspNormalizerConn intercepta frames RTSP Interleaved ($ channel len payload) na conexão TCP
// e normaliza o SSRC e SequenceNumber dos pacotes RTP de vídeo (canal 0), evitando o bug de firmware
// das câmeras que trocam o SSRC de 1 para 2 ("received packet with wrong SSRC 2, expected 1").
type rtspNormalizerConn struct {
	net.Conn
	br     *bufio.Reader
	outBuf []byte
	seq    uint16
}

func newRTSPNormalizerConn(c net.Conn) net.Conn {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetReadBuffer(512 * 1024)
	}
	return &rtspNormalizerConn{
		Conn: c,
		br:   bufio.NewReaderSize(c, 64*1024),
	}
}

func (nc *rtspNormalizerConn) Read(p []byte) (int, error) {
	if len(nc.outBuf) > 0 {
		n := copy(p, nc.outBuf)
		nc.outBuf = nc.outBuf[n:]
		return n, nil
	}

	byts, err := nc.br.Peek(2)
	if err != nil {
		return 0, err
	}

	if byts[0] == base.InterleavedFrameMagicByte {
		var fr base.InterleavedFrame
		if err := fr.Unmarshal(nc.br); err != nil {
			return 0, err
		}
		// Canal 0 = RTP de vídeo: força SSRC=1 e SequenceNumber contínuo
		if fr.Channel == 0 && len(fr.Payload) >= 12 {
			nc.seq++
			binary.BigEndian.PutUint16(fr.Payload[2:4], nc.seq)
			binary.BigEndian.PutUint32(fr.Payload[8:12], 1)
		}
		size := 4 + len(fr.Payload)
		buf := make([]byte, size)
		_, _ = fr.MarshalTo(buf)
		n := copy(p, buf)
		if n < len(buf) {
			nc.outBuf = buf[n:]
		}
		return n, nil
	}

	if byts[0] == 'R' && byts[1] == 'T' {
		var res base.Response
		if err := res.Unmarshal(nc.br); err != nil {
			return 0, err
		}
		buf, err := res.Marshal()
		if err != nil {
			return 0, err
		}
		n := copy(p, buf)
		if n < len(buf) {
			nc.outBuf = buf[n:]
		}
		return n, nil
	}

	var req base.Request
	if err := req.Unmarshal(nc.br); err != nil {
		return 0, err
	}
	buf, err := req.Marshal()
	if err != nil {
		return 0, err
	}
	n := copy(p, buf)
	if n < len(buf) {
		nc.outBuf = buf[n:]
	}
	return n, nil
}

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
	viewerJoined   atomic.Bool
	cancel         context.CancelFunc
	packetCount    uint64
	frameCount     uint64
	ingestFPS      atomic.Int64
	lastPacketNano atomic.Int64
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

	videoRTCPFeedback := []webrtc.RTCPFeedback{
		{Type: "goog-remb", Parameter: ""},
		{Type: "ccm", Parameter: "fir"},
		{Type: "nack", Parameter: ""},
		{Type: "nack", Parameter: "pli"},
	}

	// Registra suporte a H.264 com feedback RTCP completo para fluidez máxima (30/60 FPS)
	_ = m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH264,
			ClockRate:    90000,
			Channels:     0,
			SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
			RTCPFeedback: videoRTCPFeedback,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo)

	// Registra suporte a H.265 (HEVC) com feedback RTCP completo
	_ = m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH265,
			ClockRate:    90000,
			Channels:     0,
			SDPFmtpLine:  "",
			RTCPFeedback: videoRTCPFeedback,
		},
		PayloadType: 98,
	}, webrtc.RTPCodecTypeVideo)

	// Ativa interceptadores padrão WebRTC (NACK Responder para retransmissão seletiva de pacotes perdidos, RTCP Reports e TWCC)
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
		log.Printf("[Stream] Aviso ao registrar interceptadores WebRTC: %v", err)
	}

	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(m),
		webrtc.WithInterceptorRegistry(ir),
	)

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
		frmCount := atomic.LoadUint64(&cam.frameCount)
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
			"frames_received":  frmCount,
			"ingest_fps":       cam.ingestFPS.Load(),
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

		// Utiliza RTSP over TCP com normalizador de SSRC/SequenceNumber na conexão
		// e buffer de socket ampliado para garantir 100% dos P-frames e I-frames em tempo real
		transport := gortsplib.TransportTCP
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		client := &gortsplib.Client{
			Transport:    &transport,
			ReadTimeout:  8 * time.Second,
			WriteTimeout: 8 * time.Second,
			OnDecodeError: func(err error) {
				// Silencia avisos benignos de pacotes RTCP vazios/curtos enviados pelo firmware da câmera
				if err != nil && !strings.Contains(err.Error(), "rtcp:") {
					log.Printf("[RTSP] Aviso de decodificação (%s): %v", cam.id, err)
				}
			},
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				c, err := dialer.DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return newRTSPNormalizerConn(c), nil
			},
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

		// Localiza exclusivamente a track de vídeo (H.265 ou H.264)
		// Ignora tracks de áudio/metadados para economizar banda 2.4GHz e preservar SequenceNumber contínuo
		var formaH264 *format.H264
		var formaH265 *format.H265
		mediH264 := desc.FindFormat(&formaH264)
		mediH265 := desc.FindFormat(&formaH265)

		wasH265 := cam.isH265.Load()
		var codecName string
		var selectedMedia *description.Media
		var selectedFormat format.Format
		var targetTrack *webrtc.TrackLocalStaticRTP
		var targetPT uint8

		if mediH265 != nil {
			cam.isH265.Store(true)
			codecName = fmt.Sprintf("H.265 (PT %d)", formaH265.PayloadType())
			cam.setCodecInfo(codecName)
			selectedMedia = mediH265
			selectedFormat = formaH265
			targetTrack = cam.trackH265
			targetPT = 98
			log.Printf("[RTSP] ✓ Codec H.265 (HEVC) detectado para %s (%s)", cam.id, cam.name)
		} else if mediH264 != nil {
			cam.isH265.Store(false)
			codecName = fmt.Sprintf("H.264 (PT %d)", formaH264.PayloadType())
			cam.setCodecInfo(codecName)
			selectedMedia = mediH264
			selectedFormat = formaH264
			targetTrack = cam.trackH264
			targetPT = 96
			log.Printf("[RTSP] ✓ Codec H.264 detectado para %s (%s)", cam.id, cam.name)
		} else {
			cam.setError("Nenhum codec de vídeo H.264 ou H.265 encontrado no SDP da câmera")
			log.Printf("[RTSP] ❌ Nenhum formato H.264/H.265 para %s", cam.id)
			client.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		if wasH265 != cam.isH265.Load() {
			cam.closeAllSessions()
		}

		// Faz SETUP apenas da mídia de vídeo (não requisita áudio da câmera)
		_, err = client.Setup(desc.BaseURL, selectedMedia, 0, 0)
		if err != nil {
			cam.setError(fmt.Sprintf("Falha no SETUP de vídeo: %v", err))
			log.Printf("[RTSP] Falha no SETUP de %s: %v", cam.id, err)
			client.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		var lastRTPTime uint32
		var hasLastRTPTime bool
		var lastParamTS uint32
		var hasLastParamTS bool
		var curKeyframeTS uint32
		var collectingKeyframe bool
		var curKeyframePayloads [][]byte
		var lastKeyframePayloads [][]byte
		var framesInWindow int64
		var outSeq uint16
		windowStart := time.Now()

		writeRTPPayload := func(payload []byte, ts uint32, ssrc uint32, marker bool) {
			outSeq++
			p := rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					PayloadType:    targetPT,
					SequenceNumber: outSeq,
					Timestamp:      ts,
					SSRC:           ssrc,
					Marker:         marker,
				},
				Payload: payload,
			}
			_ = targetTrack.WriteRTP(&p)
		}

		// Repasse zero-copy de RTP de vídeo com injeção automática de VPS/SPS/PPS e Keyframe Instantâneo
		client.OnPacketRTP(selectedMedia, selectedFormat, func(pkt *rtp.Packet) {
			if !cam.connected.Load() {
				cam.connected.Store(true)
				cam.setError("")
			}
			cnt := atomic.AddUint64(&cam.packetCount, 1)
			now := time.Now()
			if cnt == 1 || cnt%30 == 0 {
				cam.lastPacketNano.Store(now.UnixNano())
			}

			if !hasLastRTPTime || pkt.Timestamp != lastRTPTime {
				if collectingKeyframe && len(curKeyframePayloads) > 0 {
					lastKeyframePayloads = curKeyframePayloads
					curKeyframePayloads = nil
					collectingKeyframe = false
				}
				hasLastRTPTime = true
				lastRTPTime = pkt.Timestamp
				atomic.AddUint64(&cam.frameCount, 1)
				framesInWindow++
				if elapsed := now.Sub(windowStart).Seconds(); elapsed >= 1.0 {
					cam.ingestFPS.Store(int64(float64(framesInWindow)/elapsed + 0.5))
					framesInWindow = 0
					windowStart = now
				}
			}

			// Se for H.265, captura VPS/SPS/PPS em banda (Single NALU ou AP tipo 48)
			// e injeta antes de cada Keyframe (IDR/CRA), além de garantir MTU <= 1200 bytes
			if formaH265 != nil && len(pkt.Payload) >= 2 {
				naluType := (pkt.Payload[0] >> 1) & 0x3F
				switch naluType {
				case 32: // VPS
					_, sps, pps := formaH265.SafeParams()
					formaH265.SafeSetParams(append([]byte(nil), pkt.Payload...), sps, pps)
					lastParamTS = pkt.Timestamp
					hasLastParamTS = true
				case 33: // SPS
					vps, _, pps := formaH265.SafeParams()
					formaH265.SafeSetParams(vps, append([]byte(nil), pkt.Payload...), pps)
					lastParamTS = pkt.Timestamp
					hasLastParamTS = true
				case 34: // PPS
					vps, sps, _ := formaH265.SafeParams()
					formaH265.SafeSetParams(vps, sps, append([]byte(nil), pkt.Payload...))
					lastParamTS = pkt.Timestamp
					hasLastParamTS = true
				case 48: // Aggregation Packet (AP - RFC 7798)
					off := 2
					for off+2 <= len(pkt.Payload) {
						sz := int(binary.BigEndian.Uint16(pkt.Payload[off : off+2]))
						off += 2
						if sz <= 0 || off+sz > len(pkt.Payload) {
							break
						}
						subNALU := pkt.Payload[off : off+sz]
						off += sz
						if len(subNALU) >= 2 {
							subType := (subNALU[0] >> 1) & 0x3F
							switch subType {
							case 32:
								_, sps, pps := formaH265.SafeParams()
								formaH265.SafeSetParams(append([]byte(nil), subNALU...), sps, pps)
								lastParamTS = pkt.Timestamp
								hasLastParamTS = true
							case 33:
								vps, _, pps := formaH265.SafeParams()
								formaH265.SafeSetParams(vps, append([]byte(nil), subNALU...), pps)
								lastParamTS = pkt.Timestamp
								hasLastParamTS = true
							case 34:
								vps, sps, _ := formaH265.SafeParams()
								formaH265.SafeSetParams(vps, sps, append([]byte(nil), subNALU...))
								lastParamTS = pkt.Timestamp
								hasLastParamTS = true
							}
						}
					}
				}

				isKeyframeStart := (naluType >= 19 && naluType <= 21) ||
					(naluType == 49 && len(pkt.Payload) >= 3 && (pkt.Payload[2]>>7) == 1 && ((pkt.Payload[2]&0x3F) >= 19 && (pkt.Payload[2]&0x3F) <= 21))

				// Quando um novo cliente WebRTC conecta, envia imediatamente o último Keyframe em cache
				// para que o vídeo abra em < 50ms sem esperar o próximo ciclo de GOP (4-8s) da câmera
				if cam.viewerJoined.CompareAndSwap(true, false) && !isKeyframeStart && len(lastKeyframePayloads) > 0 {
					kfTS := pkt.Timestamp - 3600
					vps, sps, pps := formaH265.SafeParams()
					for _, paramNALU := range [][]byte{vps, sps, pps} {
						if len(paramNALU) > 0 {
							writeRTPPayload(paramNALU, kfTS, pkt.SSRC, false)
						}
					}
					for idx, kfPay := range lastKeyframePayloads {
						isLast := (idx == len(lastKeyframePayloads)-1)
						writeRTPPayload(kfPay, kfTS, pkt.SSRC, isLast)
					}
				}

				if isKeyframeStart {
					collectingKeyframe = true
					curKeyframeTS = pkt.Timestamp
					curKeyframePayloads = curKeyframePayloads[:0]

					// Só injeta VPS/SPS/PPS se a câmera ainda não os enviou neste mesmo Access Unit
					if !hasLastParamTS || lastParamTS != pkt.Timestamp {
						vps, sps, pps := formaH265.SafeParams()
						for _, paramNALU := range [][]byte{vps, sps, pps} {
							if len(paramNALU) > 0 {
								writeRTPPayload(paramNALU, pkt.Timestamp, pkt.SSRC, false)
							}
						}
						lastParamTS = pkt.Timestamp
						hasLastParamTS = true
					}
				}

				isKeyframeNALU := collectingKeyframe && pkt.Timestamp == curKeyframeTS

				// Re-fragmenta pacotes RTP H.265 maiores que 1200 bytes (vindos do RTSP/TCP)
				// para nunca exceder o MTU UDP/SRTP do WebRTC na rede Wi-Fi
				const maxWebRTCPayload = 1200
				if len(pkt.Payload) > maxWebRTCPayload {
					if naluType == 49 && len(pkt.Payload) > 3 {
						// Fragmentation Unit (FU) grande -> divide mantendo cabeçalho FU (RFC 7798)
						hdr0, hdr1, fuHdr := pkt.Payload[0], pkt.Payload[1], pkt.Payload[2]
						origStart := (fuHdr >> 7) & 1
						origEnd := (fuHdr >> 6) & 1
						fuType := fuHdr & 0x3F
						data := pkt.Payload[3:]
						maxChunk := maxWebRTCPayload - 3

						for off := 0; off < len(data); off += maxChunk {
							endOff := off + maxChunk
							isFirstChunk := (off == 0)
							isLastChunk := false
							if endOff >= len(data) {
								endOff = len(data)
								isLastChunk = true
							}
							sBit := uint8(0)
							if isFirstChunk && origStart == 1 {
								sBit = 1
							}
							eBit := uint8(0)
							if isLastChunk && origEnd == 1 {
								eBit = 1
							}
							chunkPayload := make([]byte, 3+(endOff-off))
							chunkPayload[0] = hdr0
							chunkPayload[1] = hdr1
							chunkPayload[2] = (sBit << 7) | (eBit << 6) | fuType
							copy(chunkPayload[3:], data[off:endOff])

							if isKeyframeNALU && len(curKeyframePayloads) < 64 {
								curKeyframePayloads = append(curKeyframePayloads, chunkPayload)
							}
							writeRTPPayload(chunkPayload, pkt.Timestamp, pkt.SSRC, isLastChunk && pkt.Marker)
						}
						if isKeyframeNALU && pkt.Marker && len(curKeyframePayloads) > 0 {
							lastKeyframePayloads = curKeyframePayloads
							curKeyframePayloads = nil
							collectingKeyframe = false
						}
						return
					} else if naluType >= 1 && naluType <= 47 {
						// Single NALU grande -> converte em fragmentos FU (Type 49) <= 1200 bytes
						hdr0, hdr1 := pkt.Payload[0], pkt.Payload[1]
						data := pkt.Payload[2:]
						maxChunk := maxWebRTCPayload - 3

						for off := 0; off < len(data); off += maxChunk {
							endOff := off + maxChunk
							isFirstChunk := (off == 0)
							isLastChunk := false
							if endOff >= len(data) {
								endOff = len(data)
								isLastChunk = true
							}
							sBit := uint8(0)
							if isFirstChunk {
								sBit = 1
							}
							eBit := uint8(0)
							if isLastChunk {
								eBit = 1
							}
							chunkPayload := make([]byte, 3+(endOff-off))
							chunkPayload[0] = (hdr0 & 0b10000001) | (49 << 1)
							chunkPayload[1] = hdr1
							chunkPayload[2] = (sBit << 7) | (eBit << 6) | naluType
							copy(chunkPayload[3:], data[off:endOff])

							if isKeyframeNALU && len(curKeyframePayloads) < 64 {
								curKeyframePayloads = append(curKeyframePayloads, chunkPayload)
							}
							writeRTPPayload(chunkPayload, pkt.Timestamp, pkt.SSRC, isLastChunk && pkt.Marker)
						}
						if isKeyframeNALU && pkt.Marker && len(curKeyframePayloads) > 0 {
							lastKeyframePayloads = curKeyframePayloads
							curKeyframePayloads = nil
							collectingKeyframe = false
						}
						return
					}
				}

				if isKeyframeNALU && len(curKeyframePayloads) < 64 {
					curKeyframePayloads = append(curKeyframePayloads, append([]byte(nil), pkt.Payload...))
					if pkt.Marker && len(curKeyframePayloads) > 0 {
						lastKeyframePayloads = curKeyframePayloads
						curKeyframePayloads = nil
						collectingKeyframe = false
					}
				}
			}

			writeRTPPayload(pkt.Payload, pkt.Timestamp, pkt.SSRC, pkt.Marker)
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
