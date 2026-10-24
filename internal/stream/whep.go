package stream

import (
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

	cam, exists := e.GetCamera(cameraID)
	if !exists {
		http.Error(w, fmt.Sprintf("Câmera '%s' não encontrada", cameraID), http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) == 0 {
			http.Error(w, "SDP Offer ausente ou corpo da requisição vazio", http.StatusBadRequest)
			return
		}

		offerSDP := string(body)

		// Cria uma nova conexão PeerConnection
		pc, err := e.api.NewPeerConnection(webrtc.Configuration{
			SDPSemantics: webrtc.SDPSemanticsUnifiedPlan,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("Erro ao instanciar PeerConnection: %v", err), http.StatusInternalServerError)
			return
		}

		// Adiciona a track H.264 da câmera à conexão
		if _, err := pc.AddTrack(cam.track); err != nil {
			_ = pc.Close()
			http.Error(w, fmt.Sprintf("Erro ao adicionar track de vídeo: %v", err), http.StatusInternalServerError)
			return
		}

		sessionID := generateSessionID()

		// Registra callback de mudança de estado para liberar recursos em caso de desconexão
		pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
			if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
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
		w.Header().Set("Location", fmt.Sprintf("/api/whep/%s/session/%s", cameraID, sessionID))
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
		}
		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
	}
}
