package stream

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"argus/internal/config"
	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/webrtc/v4"
)

// TestDiagnostic_CameraConfiguration valida o carregamento de configurações e URLs
func TestDiagnostic_CameraConfiguration(t *testing.T) {
	t.Log("==================================================================")
	t.Log("🔍 DIAGNÓSTICO 1: Verificação de Configurações e Variáveis (.env)")
	t.Log("==================================================================")

	cfg, err := config.LoadConfig("../../config.example.yaml")
	if err != nil {
		t.Fatalf("❌ Falha ao carregar configuração: %v", err)
	}

	if len(cfg.Cameras) == 0 {
		t.Fatalf("❌ Nenhuma câmera configurada no arquivo de configuração!")
	}

	for _, cam := range cfg.Cameras {
		masked := maskURL(cam.RTSPURL)
		if cam.RTSPURL == "" {
			t.Errorf("❌ Câmera '%s' (%s) está com RTSPURL VAZIA!", cam.ID, cam.Name)
		} else if strings.HasPrefix(cam.RTSPURL, "${") {
			t.Errorf("❌ Câmera '%s' (%s) com variável não interpolada: %s", cam.ID, cam.Name, cam.RTSPURL)
		} else {
			t.Logf("✓ [%s] %s -> URL configurada: %s", cam.ID, cam.Name, masked)
		}
	}
}

// TestDiagnostic_CameraTCPReachability testa conectividade TCP direta na porta 554
func TestDiagnostic_CameraTCPReachability(t *testing.T) {
	t.Log("==================================================================")
	t.Log("🌐 DIAGNÓSTICO 2: Conectividade de Rede TCP (Porta 554)")
	t.Log("==================================================================")

	cfg, err := config.LoadConfig("../../config.example.yaml")
	if err != nil {
		t.Fatalf("❌ Falha ao carregar configuração: %v", err)
	}

	for _, cam := range cfg.Cameras {
		raw := strings.TrimSpace(cam.RTSPURL)
		if idx := strings.Index(raw, "#"); idx != -1 {
			raw = raw[:idx]
		}
		u, err := base.ParseURL(raw)
		if err != nil {
			t.Logf("⚠️ [%s] Erro ao parsear URL: %v", cam.ID, err)
			continue
		}

		host := u.Host
		if !strings.Contains(host, ":") {
			host = net.JoinHostPort(host, "554")
		}

		start := time.Now()
		conn, err := net.DialTimeout("tcp", host, 2*time.Second)
		duration := time.Since(start)

		if err != nil {
			t.Logf("❌ [%s] %s (%s) INACESSÍVEL: %v", cam.ID, cam.Name, host, err)
			t.Logf("   -> Dica: Verifique se o IP %s está na mesma rede e se a câmera está ligada.", host)
		} else {
			_ = conn.Close()
			t.Logf("✓ [%s] %s (%s) -> TCP 554 ABERTO (Latência: %v)", cam.ID, cam.Name, host, duration.Round(time.Millisecond))
		}
	}
}

// TestDiagnostic_RTSPHandshake realiza o handshake RTSP DESCRIBE com as credenciais
func TestDiagnostic_RTSPHandshake(t *testing.T) {
	t.Log("==================================================================")
	t.Log("🔐 DIAGNÓSTICO 3: Handshake RTSP DESCRIBE & Autenticação de Câmera")
	t.Log("==================================================================")

	cfg, err := config.LoadConfig("../../config.example.yaml")
	if err != nil {
		t.Fatalf("❌ Falha ao carregar configuração: %v", err)
	}

	for _, cam := range cfg.Cameras {
		raw := strings.TrimSpace(cam.RTSPURL)
		if idx := strings.Index(raw, "#"); idx != -1 {
			raw = raw[:idx]
		}
		u, err := base.ParseURL(raw)
		if err != nil {
			continue
		}

		host := u.Host
		if !strings.Contains(host, ":") {
			host = net.JoinHostPort(host, "554")
		}

		// Pré-teste TCP rápido
		conn, err := net.DialTimeout("tcp", host, 1*time.Second)
		if err != nil {
			t.Logf("⏭️ [%s] Pulando teste RTSP (TCP inacessível)", cam.ID)
			continue
		}
		_ = conn.Close()

		transport := gortsplib.TransportTCP
		client := &gortsplib.Client{
			Transport: &transport,
		}

		err = client.Start(u.Scheme, u.Host)
		if err != nil {
			t.Logf("❌ [%s] Falha no client.Start: %v", cam.ID, err)
			continue
		}

		desc, _, err := client.Describe(u)
		if err != nil {
			t.Logf("❌ [%s] Falha no DESCRIBE (Autenticação/Credenciais): %v", cam.ID, err)
			t.Logf("   -> Dica: Verifique usuário e senha no .env para a câmera %s", cam.ID)
			client.Close()
			continue
		}

		t.Logf("📋 [%s] Medias encontradas no SDP (%d):", cam.ID, len(desc.Medias))
		for i, m := range desc.Medias {
			t.Logf("   • Media %d: Tipo=%s, Control=%s", i, m.Type, m.Control)
			for j, f := range m.Formats {
				t.Logf("     - Format %d: %T (Codec=%s, ClockRate=%d, PayloadType=%d)",
					j, f, f.Codec(), f.ClockRate(), f.PayloadType())
			}
		}

		var formaH264 *format.H264
		var formaH265 *format.H265
		hasH264 := desc.FindFormat(&formaH264) != nil
		hasH265 := desc.FindFormat(&formaH265) != nil

		if hasH264 {
			t.Logf("✓ [%s] Suporta H.264 (PT: %d)", cam.ID, formaH264.PayloadType())
		}
		if hasH265 {
			t.Logf("✓ [%s] Suporta H.265 (PT: %d)", cam.ID, formaH265.PayloadType())
		}

		client.Close()

		// Testa se a câmera possui sub-stream (/stream2)
		urlStream2 := strings.Replace(raw, "/stream1", "/stream2", 1)
		if u2, err := base.ParseURL(urlStream2); err == nil {
			c2 := &gortsplib.Client{Transport: &transport}
			if err := c2.Start(u2.Scheme, u2.Host); err == nil {
				if d2, _, err := c2.Describe(u2); err == nil {
					var h264Sub *format.H264
					var h265Sub *format.H265
					s2HasH264 := d2.FindFormat(&h264Sub) != nil
					s2HasH265 := d2.FindFormat(&h265Sub) != nil
					t.Logf("📡 [%s] Sub-stream (/stream2) DISPONÍVEL! Codecs: H.264=%v, H.265=%v",
						cam.ID, s2HasH264, s2HasH265)
				}
				c2.Close()
			}
		}
	}
}

// TestDiagnostic_LocalGo2RTC verifica a presença do gateway go2rtc local
func TestDiagnostic_LocalGo2RTC(t *testing.T) {
	t.Log("==================================================================")
	t.Log("⚡ DIAGNÓSTICO 4: Gateway Local go2rtc (Porta 1984)")
	t.Log("==================================================================")

	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get("http://127.0.0.1:1984/api/streams")
	if err != nil {
		t.Log("ℹ️ go2rtc local não detectado em 127.0.0.1:1984 (Argus operará 100% autônomo via motor próprio)")
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	t.Logf("✓ Gateway go2rtc DETECTADO E ATIVO na porta 1984!")

	var streams map[string]interface{}
	if err := json.Unmarshal(body, &streams); err == nil {
		t.Logf("   Streams ativos no go2rtc: %d", len(streams))
		for name := range streams {
			t.Logf("   • Stream: %s", name)
		}
	}
}

// TestDiagnostic_WebRTCWHEPLoopback valida a negociação WHEP do Argus
func TestDiagnostic_WebRTCWHEPLoopback(t *testing.T) {
	t.Log("==================================================================")
	t.Log("🛡️ DIAGNÓSTICO 5: Loopback de Negociação WebRTC WHEP")
	t.Log("==================================================================")

	cameras := []config.CameraConfig{
		{ID: "cam_frente", Name: "Câmera Portão", RTSPURL: "rtsp://localhost:8554/cam1"},
	}

	engine := NewEngine(cameras)
	defer engine.Close()

	clientPC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("Erro ao instanciar cliente WebRTC de teste: %v", err)
	}
	defer clientPC.Close()

	_, err = clientPC.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		t.Fatalf("Erro ao adicionar transceiver: %v", err)
	}

	offer, err := clientPC.CreateOffer(nil)
	if err != nil {
		t.Fatalf("Erro ao criar oferta SDP: %v", err)
	}
	if err := clientPC.SetLocalDescription(offer); err != nil {
		t.Fatalf("Erro ao aplicar local description: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/whep/cam_frente", bytes.NewBufferString(offer.SDP))
	req.Header.Set("Content-Type", "application/sdp")

	engine.ServeWHEP(rec, req, "cam_frente")

	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("Falha na negociação WHEP. Código recebido: %d, Body: %s", rec.Code, rec.Body.String())
	}

	answerSDP := rec.Body.String()
	if !strings.Contains(answerSDP, "m=video") {
		t.Fatalf("SDP Answer não contém m=video")
	}

	t.Log("✓ Negociação WHEP SDP Offer -> SDP Answer concluída com sucesso (m=video presente)!")
}

// TestDiagnostic_LiveStreamingIngest conecta o motor às câmeras reais e mede a recepção de pacotes RTP
func TestDiagnostic_LiveStreamingIngest(t *testing.T) {
	t.Log("==================================================================")
	t.Log("📹 DIAGNÓSTICO 6: Ingestão de Vídeo em Tempo Real (Recepção RTP)")
	t.Log("==================================================================")

	cfg, err := config.LoadConfig("../../config.example.yaml")
	if err != nil {
		t.Fatalf("❌ Falha ao carregar configuração: %v", err)
	}

	engine := NewEngine(cfg.Cameras)
	defer engine.Close()

	t.Log("⏳ Aguardando 3 segundos para coletar fluxo de pacotes das câmeras ativas...")
	time.Sleep(3 * time.Second)

	cams := engine.ListCameras()
	t.Logf("📋 Status dos Streams após 3 segundos:")
	for _, c := range cams {
		t.Logf("   • [%s] %s:", c["id"], c["name"])
		t.Logf("     - Conectado: %v", c["connected"])
		t.Logf("     - Codec: %v", c["codec"])
		t.Logf("     - Pacotes RTP Recebidos: %v", c["packets_received"])
		t.Logf("     - Último Erro: %v", c["last_error"])
	}
}
