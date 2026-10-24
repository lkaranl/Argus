package telemetry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"argus/internal/config"
	"argus/internal/solar"
	"argus/internal/sse"
	"argus/internal/weather"
)

func TestTelemetryManager(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 8080},
		Cameras: []config.CameraConfig{
			{ID: "cam1", Name: "Câmera 1"},
			{ID: "cam2", Name: "Câmera 2"},
		},
		Solar: config.SolarConfig{
			Enabled:             false, // Desativado no teste para retorno rápido
			PollIntervalMinutes: 5,
		},
		Weather: config.WeatherConfig{
			Enabled:             false,
			PollIntervalMinutes: 30,
		},
	}

	broker := sse.NewBroker()
	solarClient := solar.NewClient(solar.Config{Enabled: false})
	weatherClient := weather.NewClient(weather.Config{Enabled: false})

	mgr := NewManager(cfg, solarClient, weatherClient, broker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Inicia o manager
	go mgr.Start(ctx)

	// Aguarda ciclo inicial
	time.Sleep(100 * time.Millisecond)

	latest := mgr.GetLatest()
	if latest == nil {
		t.Fatal("Esperava telemetria combinada não nula")
	}

	if latest.Server.CamerasTotal != 2 {
		t.Errorf("Esperava 2 câmeras, obteve %d", latest.Server.CamerasTotal)
	}

	payload, err := json.Marshal(latest)
	if err != nil {
		t.Fatalf("Erro ao serializar telemetria: %v", err)
	}

	strPayload := string(payload)
	if !strings.Contains(strPayload, "uptime_seconds") {
		t.Errorf("Payload não contém 'uptime_seconds': %s", strPayload)
	}
}
