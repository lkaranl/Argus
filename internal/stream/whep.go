package stream

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
)

func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func tryGo2RTC(cameraID string, sdpOffer []byte) ([]byte, bool) {
	client := &http.Client{Timeout: 2000 * time.Millisecond}
	baseID := strings.TrimSuffix(cameraID, "_sub")
	var candidates []string

	switch baseID {
	case "cam_frente", "camera_1":
		candidates = append(candidates, "cam-0", "cam_frente", "camera-1")
	case "cam_garagem", "camera_2":
		candidates = append(candidates, "cam-1", "cam_garagem", "camera-2")
	case "cam_fundos", "camera_3":
		candidates = append(candidates, "cam-2", "cam_fundos", "camera-3")
	default:
		candidates = append(candidates, baseID, cameraID)
	}

	for _, cand := range candidates {
		// go2rtc suporta tanto a API WebRTC padrão (/api/webrtc) quanto WHEP (/api/whep)
		urls := []string{
			fmt.Sprintf("http://127.0.0.1:1984/api/webrtc?src=%s", cand),
			fmt.Sprintf("http://127.0.0.1:1984/api/whep?src=%s&media=video", cand),
		}

		for _, go2rtcURL := range urls {
			req, err := http.NewRequest(http.MethodPost, go2rtcURL, bytes.NewReader(sdpOffer))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/sdp")
			resp, err := client.Do(req)
			if err == nil {
				if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
					ans, _ := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if len(ans) > 0 {
						return ans, true
					}
				}
				_ = resp.Body.Close()
			}
		}
	}
	return nil, false
}

// ServeWHEP implementa a especificação WHEP (WebRTC HTTP Egress Protocol)
func (e *Engine) ServeWHEP(w http.ResponseWriter, r *http.Request, cameraID string) {
	// CORS e cabeçalhos WHEP para preflight
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, If-Match")
	w.Header().Set("Accept-Post", "application/sdp")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	targetID := cameraID
	streamReq := r.URL.Query().Get("stream")
	if streamReq == "sub" || strings.HasSuffix(cameraID, "_sub") {
		subID := strings.TrimSuffix(cameraID, "_sub") + "_sub"
		if _, exists := e.GetCamera(subID); exists {
			targetID = subID
		}
	} else if streamReq == "main" {
		targetID = strings.TrimSuffix(cameraID, "_sub")
	}

	cam, exists := e.GetCamera(targetID)
	if !exists {
		// Fallback para a câmera solicitada
		cam, exists = e.GetCamera(cameraID)
		if !exists {
			http.Error(w, fmt.Sprintf("Câmera '%s' não encontrada", cameraID), http.StatusNotFound)
			return
		}
		targetID = cameraID
	}

	switch r.Method {
	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) == 0 {
			http.Error(w, "SDP Offer ausente ou corpo da requisição vazio", http.StatusBadRequest)
			return
		}

		offerSDP := string(body)
		clientSupportsH265 := strings.Contains(strings.ToLower(offerSDP), "h265")

		// Se o stream nativo ainda não estiver conectado OU se a câmera transmitir em H.265
		// e o navegador do cliente (ex: Chromium / Firefox no Linux/Raspberry Pi) NÃO suportar H.265,
		// delega automaticamente para o gateway go2rtc local que possui fallback de transcodificação FFmpeg para H.264
		if !cam.connected.Load() || (cam.isH265.Load() && !clientSupportsH265) {
			if ans, ok := tryGo2RTC(targetID, body); ok {
				w.Header().Set("Content-Type", "application/sdp")
				w.Header().Set("Location", fmt.Sprintf("/api/whep/%s/session/%s", targetID, generateSessionID()))
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write(ans)
				return
			}
		}

		// Cria uma nova conexão PeerConnection
		pc, err := e.api.NewPeerConnection(webrtc.Configuration{
			SDPSemantics: webrtc.SDPSemanticsUnifiedPlan,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("Erro ao instanciar PeerConnection: %v", err), http.StatusInternalServerError)
			return
		}

		// Escolhe a track correspondente ao codec ativo da câmera (H.265 ou H.264)
		trackToSend := cam.trackH264
		if cam.isH265.Load() {
			trackToSend = cam.trackH265
		}

		// Adiciona a track de vídeo da câmera à conexão
		sender, err := pc.AddTrack(trackToSend)
		if err != nil {
			_ = pc.Close()
			http.Error(w, fmt.Sprintf("Erro ao adicionar track de vídeo: %v", err), http.StatusInternalServerError)
			return
		}

		// Consome pacotes RTCP recebidos do navegador em background
		go func() {
			rtcpBuf := make([]byte, 1500)
			for {
				if _, _, err := sender.Read(rtcpBuf); err != nil {
					return
				}
			}
		}()

		sessionID := generateSessionID()

		// Registra callback de mudança de estado para envio imediato de Keyframe e limpeza na desconexão
		pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
			if state == webrtc.PeerConnectionStateConnected {
				cam.viewerJoined.Store(true)
			} else if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
				cam.sessMu.Lock()
				delete(cam.sessions, sessionID)
				cam.sessMu.Unlock()
				_ = pc.Close()
			}
		})

		// Seta a SDP Offer recebida do cliente
		if err := pc.SetRemoteDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  offerSDP,
		}); err != nil {
			_ = pc.Close()
			http.Error(w, fmt.Sprintf("SDP Offer inválida: %v", err), http.StatusBadRequest)
			return
		}

		// Cria a SDP Answer
		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			_ = pc.Close()
			http.Error(w, fmt.Sprintf("Erro ao gerar SDP Answer: %v", err), http.StatusInternalServerError)
			return
		}

		// Aplica Local Description
		gatherComplete := webrtc.GatheringCompletePromise(pc)
		if err := pc.SetLocalDescription(answer); err != nil {
			_ = pc.Close()
			http.Error(w, fmt.Sprintf("Erro ao aplicar Local Description: %v", err), http.StatusInternalServerError)
			return
		}

		// Aguarda coleta inicial de candidatos ICE locais (timeout de 1s para resposta rápida)
		select {
		case <-gatherComplete:
		case <-time.After(1 * time.Second):
		case <-r.Context().Done():
			_ = pc.Close()
			return
		}

		// Armazena sessão ativa
		cam.sessMu.Lock()
		cam.sessions[sessionID] = pc
		cam.sessMu.Unlock()

		// Responde com formato padrão WHEP
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", fmt.Sprintf("/api/whep/%s/session/%s", targetID, sessionID))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(pc.LocalDescription().SDP))

	case http.MethodDelete:
		// Encerramento de sessão WHEP
		parts := strings.Split(r.URL.Path, "/session/")
		if len(parts) == 2 {
			sessionID := parts[1]
			cam.sessMu.Lock()
			if pc, ok := cam.sessions[sessionID]; ok {
				_ = pc.Close()
				delete(cam.sessions, sessionID)
			}
			cam.sessMu.Unlock()

			// Também tenta no par mainstream/substream associado
			altID := strings.TrimSuffix(targetID, "_sub")
			if altID == targetID {
				altID = targetID + "_sub"
			}
			if altCam, ok := e.GetCamera(altID); ok {
				altCam.sessMu.Lock()
				if pc, ok := altCam.sessions[sessionID]; ok {
					_ = pc.Close()
					delete(altCam.sessions, sessionID)
				}
				altCam.sessMu.Unlock()
			}
		}
		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
	}
}
