package stream

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"argus/internal/config"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/pion/webrtc/v4"
)

func TestRTSPNormalizerConn(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	norm := newRTSPNormalizerConn(c1)

	go func() {
		// Envia dois frames RTSP Interleaved no canal 0 com SSRC diferentes (1 e 2) e SequenceNumber fora de ordem
		for i, ssrc := range []uint32{1, 2} {
			rtpPkt := make([]byte, 14)
			rtpPkt[0] = 0x80
			rtpPkt[1] = 98
			binary.BigEndian.PutUint16(rtpPkt[2:4], uint16(500+i*10))
			binary.BigEndian.PutUint32(rtpPkt[4:8], 90000)
			binary.BigEndian.PutUint32(rtpPkt[8:12], ssrc)
			rtpPkt[12] = 0x40
			rtpPkt[13] = 0x01

			fr := base.InterleavedFrame{
				Channel: 0,
				Payload: rtpPkt,
			}
			buf := make([]byte, 4+len(rtpPkt))
			_, _ = fr.MarshalTo(buf)
			_, _ = c2.Write(buf)
		}
	}()

	buf := make([]byte, 64)
	for expectedSeq := uint16(1); expectedSeq <= 2; expectedSeq++ {
		n, err := norm.Read(buf)
		if err != nil {
			t.Fatalf("Erro ao ler frame normalizado %d: %v", expectedSeq, err)
		}
		if n < 16 {
			t.Fatalf("Frame curto demais: %d bytes", n)
		}
		gotSeq := binary.BigEndian.Uint16(buf[6:8])
		gotSSRC := binary.BigEndian.Uint32(buf[12:16])
		if gotSeq != expectedSeq {
			t.Errorf("SequenceNumber esperado %d, obteve %d", expectedSeq, gotSeq)
		}
		if gotSSRC != 1 {
			t.Errorf("SSRC esperado 1, obteve %d", gotSSRC)
		}
	}
}

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

	// 4. Teste de Substream sob Demanda e Fallback gracioso
	camsWithSub := []config.CameraConfig{
		{
			ID:           "cam_garagem",
			Name:         "Câmera Garagem",
			RTSPURL:      "rtsp://localhost:8554/cam_garagem_main",
			SubstreamURL: "rtsp://localhost:8554/cam_garagem_sub",
		},
	}
	engineWithSub := NewEngine(camsWithSub)

	// 4a. Solicitar ?stream=sub para câmera com substream configurado
	recSub := httptest.NewRecorder()
	reqSub := httptest.NewRequest("POST", "/api/whep/cam_garagem?stream=sub", bytes.NewBufferString(offer.SDP))
	reqSub.Header.Set("Content-Type", "application/sdp")
	engineWithSub.ServeWHEP(recSub, reqSub, "cam_garagem")

	if recSub.Code != http.StatusCreated && recSub.Code != http.StatusOK {
		t.Fatalf("Esperava 201/200 para substream, obteve %d: %s", recSub.Code, recSub.Body.String())
	}
	if loc := recSub.Header().Get("Location"); !strings.Contains(loc, "cam_garagem_sub") {
		t.Errorf("Esperava Location contendo 'cam_garagem_sub', obteve '%s'", loc)
	}

	// 4b. Solicitar ?stream=main para câmera com substream configurado
	recMain := httptest.NewRecorder()
	reqMain := httptest.NewRequest("POST", "/api/whep/cam_garagem?stream=main", bytes.NewBufferString(offer.SDP))
	reqMain.Header.Set("Content-Type", "application/sdp")
	engineWithSub.ServeWHEP(recMain, reqMain, "cam_garagem")

	if recMain.Code != http.StatusCreated && recMain.Code != http.StatusOK {
		t.Fatalf("Esperava 201/200 para mainstream, obteve %d: %s", recMain.Code, recMain.Body.String())
	}

	// 4c. Solicitar ?stream=sub para câmera SEM substream (deve fazer fallback transparente)
	recFallback := httptest.NewRecorder()
	reqFallback := httptest.NewRequest("POST", "/api/whep/cam_frente?stream=sub", bytes.NewBufferString(offer.SDP))
	reqFallback.Header.Set("Content-Type", "application/sdp")
	engine.ServeWHEP(recFallback, reqFallback, "cam_frente")

	if recFallback.Code != http.StatusCreated && recFallback.Code != http.StatusOK {
		t.Fatalf("Esperava 201/200 no fallback de substream, obteve %d: %s", recFallback.Code, recFallback.Body.String())
	}
}
