package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"argus/internal/config"
	"argus/internal/solar"
	"argus/internal/sse"
	"argus/internal/stream"
	"argus/internal/telemetry"
	"argus/internal/weather"
)

func TestServerRoutes(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080, BindAddress: "0.0.0.0"},
		Cameras: []config.CameraConfig{
			{ID: "cam_frente", Name: "Câmera 01"},
		},
		Solar:   config.SolarConfig{Enabled: false},
		Weather: config.WeatherConfig{Enabled: false},
	}

	broker := sse.NewBroker()
	solarClient := solar.NewClient(solar.Config{Enabled: false})
	weatherClient := weather.NewClient(weather.Config{Enabled: false})
	telemetryMgr := telemetry.NewManager(cfg, solarClient, weatherClient, broker)
	streamEngine := stream.NewEngine(cfg.Cameras)
	defer streamEngine.Close()

	router := setupRouter(cfg, broker, telemetryMgr, streamEngine)

	// 1. Testa rota estática / (index.html)
	reqRoot := httptest.NewRequest("GET", "/", nil)
	recRoot := httptest.NewRecorder()
	router.ServeHTTP(recRoot, reqRoot)

	if recRoot.Code != http.StatusOK {
		t.Errorf("Rota raiz retornou status %d", recRoot.Code)
	}
	if !strings.Contains(recRoot.Body.String(), "ARGUS — CCTV Command Hub") {
		t.Errorf("HTML da página inicial não contém o título do Argus")
	}

	// 2. Testa rota de status da API /api/status
	reqStatus := httptest.NewRequest("GET", "/api/status", nil)
	recStatus := httptest.NewRecorder()
	router.ServeHTTP(recStatus, reqStatus)

	if recStatus.Code != http.StatusOK {
		t.Errorf("Rota /api/status retornou status %d", recStatus.Code)
	}

	var statusResp map[string]interface{}
	if err := json.Unmarshal(recStatus.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("Erro ao decodificar JSON de /api/status: %v", err)
	}

	if statusResp["status"] != "online" {
		t.Errorf("Esperava status 'online', obteve %v", statusResp["status"])
	}

	// 3. Testa rota de lista de câmeras /api/cameras
	reqCams := httptest.NewRequest("GET", "/api/cameras", nil)
	recCams := httptest.NewRecorder()
	router.ServeHTTP(recCams, reqCams)

	if recCams.Code != http.StatusOK {
		t.Errorf("Rota /api/cameras retornou status %d", recCams.Code)
	}

	// 4. Testa rota de histórico solar /api/solar/history
	reqHistory := httptest.NewRequest("GET", "/api/solar/history?days=7", nil)
	recHistory := httptest.NewRecorder()
	router.ServeHTTP(recHistory, reqHistory)

	if recHistory.Code != http.StatusOK {
		t.Errorf("Rota /api/solar/history retornou status %d", recHistory.Code)
	}

	var histResp map[string][]map[string]interface{}
	if err := json.Unmarshal(recHistory.Body.Bytes(), &histResp); err != nil {
		t.Fatalf("Erro ao decodificar JSON de /api/solar/history: %v", err)
	}
	if len(histResp["days"]) != 7 {
		t.Errorf("Esperava 7 slots de histórico solar, obteve %d", len(histResp["days"]))
	}
}
