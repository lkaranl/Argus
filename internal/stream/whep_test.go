package stream

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"argus/internal/config"
	"github.com/pion/webrtc/v4"
)

func TestWHEPHandler(t *testing.T) {
	cameras := []config.CameraConfig{
		{ID: "cam_frente", Name: "Câmera Frente", RTSPURL: "rtsp://localhost:8554/cam1"},
	}

	engine := NewEngine(cameras)

	// Cria um PeerConnection cliente para gerar uma oferta SDP real
	clientPC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("Erro ao criar PeerConnection de teste: %v", err)
	}
	defer clientPC.Close()

	// Adiciona transceiver de vídeo (recvonly) como o navegador faz via WHEP
	_, err = clientPC.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		t.Fatalf("Erro ao adicionar transceiver: %v", err)
	}

	offer, err := clientPC.CreateOffer(nil)
	if err != nil {
		t.Fatalf("Erro ao gerar SDP Offer: %v", err)
	}
	if err := clientPC.SetLocalDescription(offer); err != nil {
		t.Fatalf("Erro ao setar local description: %v", err)
	}

	// 1. Teste de sucesso: POST /api/whep/cam_frente
	req := httptest.NewRequest("POST", "/api/whep/cam_frente", bytes.NewBufferString(offer.SDP))
	req.Header.Set("Content-Type", "application/sdp")
	rec := httptest.NewRecorder()

	engine.ServeWHEP(rec, req, "cam_frente")

	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("Esperava status 201 ou 200, obteve %d. Body: %s", rec.Code, rec.Body.String())
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/sdp") {
		t.Errorf("Content-Type esperado 'application/sdp', obteve '%s'", contentType)
	}

	answerSDP := rec.Body.String()
	if !strings.Contains(answerSDP, "m=video") {
		t.Errorf("SDP Answer não contém media video: %s", answerSDP)
	}

	// 2. Teste de câmera inexistente: 404 Not Found
	rec404 := httptest.NewRecorder()
	req404 := httptest.NewRequest("POST", "/api/whep/cam_fantasma", bytes.NewBufferString(offer.SDP))
	engine.ServeWHEP(rec404, req404, "cam_fantasma")

	if rec404.Code != http.StatusNotFound {
		t.Errorf("Esperava 404 para câmera inexistente, obteve %d", rec404.Code)
	}

	// 3. Teste de método não permitido: 405 Method Not Allowed
	rec405 := httptest.NewRecorder()
	req405 := httptest.NewRequest("PUT", "/api/whep/cam_frente", nil)
	engine.ServeWHEP(rec405, req405, "cam_frente")

	if rec405.Code != http.StatusMethodNotAllowed {
		t.Errorf("Esperava 405 para método PUT, obteve %d", rec405.Code)
	}
}
