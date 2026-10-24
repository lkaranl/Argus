package config

import (
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Cria arquivo temporário de config.yaml
	yamlContent := `
server:
  port: 8080
  bind_address: "0.0.0.0"

cameras:
  - id: "cam_frente"
    name: "Câmera 01 — Frente"
    rtsp_url: "${CAM_1_RTSP}"
  - id: "cam_garagem"
    name: "Câmera 02 — Garagem"
    rtsp_url: "rtsp://user:pass@192.168.1.102:554/live"

solar:
  poll_interval_minutes: 5

weather:
  enabled: true
  latitude: -10.8778
  longitude: -61.9514
  city_name: "Ji-Paraná, RO"
  poll_interval_minutes: 30
`
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Erro ao criar arquivo temporário: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write([]byte(yamlContent)); err != nil {
		t.Fatalf("Erro ao escrever arquivo temporário: %v", err)
	}
	tmpFile.Close()

	// Define variáveis de ambiente para teste
	os.Setenv("CAM_1_RTSP", "rtsp://admin:123456@192.168.1.101:554/stream1")
	os.Setenv("FUSIONSOLAR_TOKEN", "TEST_TOKEN_1234567890123456")
	os.Setenv("FUSIONSOLAR_KIOSK_URL", "https://intl.fusionsolar.huawei.com/pvmswebsite/nologin/assets/build/cloud.html#/kiosk?kk=TEST_TOKEN_1234567890123456")
	defer func() {
		os.Unsetenv("CAM_1_RTSP")
		os.Unsetenv("FUSIONSOLAR_TOKEN")
		os.Unsetenv("FUSIONSOLAR_KIOSK_URL")
	}()

	cfg, err := LoadConfig(tmpFile.Name())
	if err != nil {
		t.Fatalf("Falha ao carregar configuração: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("Porta esperada 8080, obteve %d", cfg.Server.Port)
	}

	if len(cfg.Cameras) != 2 {
		t.Fatalf("Esperava 2 câmeras, obteve %d", len(cfg.Cameras))
	}

	if cfg.Cameras[0].RTSPURL != "rtsp://admin:123456@192.168.1.101:554/stream1" {
		t.Errorf("Interpolação de variável de ambiente falhou. Obteve: %s", cfg.Cameras[0].RTSPURL)
	}

	if cfg.Solar.Token != "TEST_TOKEN_1234567890123456" {
		t.Errorf("Token solar esperado TEST_TOKEN_1234567890123456, obteve: %s", cfg.Solar.Token)
	}

	if cfg.Weather.CityName != "Ji-Paraná, RO" {
		t.Errorf("Nome da cidade esperado Ji-Paraná, RO, obteve: %s", cfg.Weather.CityName)
	}
}
