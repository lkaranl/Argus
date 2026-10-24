package solar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFusionSolarClient(t *testing.T) {
	// Payload simulando o retorno real da Huawei com HTML entities escapadas (&quot;)
	rawJSON := `{
		"success": true,
		"data": "{\"realKpi\":{\"realTimePower\":3.65,\"dailyEnergy\":14.2,\"monthEnergy\":350.5,\"yearEnergy\":4200.0,\"cumulativeEnergy\":12500.0},\"stationOverview\":{\"stationName\":\"Usina Solar Karan\",\"plantAddress\":\"Ji-Paraná, RO\"},\"socialContribution\":{\"co2Reduction\":11250.0,\"equivalentTreePlanting\":620}}"
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("kk")
		if token != "TEST_TOKEN_123" {
			http.Error(w, "Token inválido", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rawJSON))
	}))
	defer server.Close()

	client := NewClient(Config{
		Enabled:  true,
		Token:    "TEST_TOKEN_123",
		KioskURL: server.URL + "/rest/pvms/web/kiosk/v1/station-kiosk-file?kk=TEST_TOKEN_123",
	})

	data, err := client.FetchData(context.Background())
	if err != nil {
		t.Fatalf("Erro ao consultar telemetria solar: %v", err)
	}

	if !data.Available {
		t.Fatal("Esperava dados disponíveis (Available = true)")
	}

	if data.PowerKW != 3.65 {
		t.Errorf("Potência esperada 3.65 kW, obteve %f", data.PowerKW)
	}

	if data.YieldTodayKWh != 14.2 {
		t.Errorf("Geração do dia esperada 14.2 kWh, obteve %f", data.YieldTodayKWh)
	}

	if data.StationName != "Usina Solar Karan" {
		t.Errorf("Nome da usina esperado 'Usina Solar Karan', obteve '%s'", data.StationName)
	}

	if data.CO2Reduction != "11.25 t" {
		t.Errorf("Redução de CO2 esperada '11.25 t', obteve '%s'", data.CO2Reduction)
	}

	if data.TreesPlanted != "620" {
		t.Errorf("Árvores esperadas '620', obteve '%s'", data.TreesPlanted)
	}
}

func TestFusionSolarClientTokenExtractor(t *testing.T) {
	urlWithKK := "https://intl.fusionsolar.huawei.com/pvmswebsite/nologin/assets/build/cloud.html#/kiosk?kk=EM6wrnyAhBtOFvD7rdNu045xGpwb1bGD"
	token := ExtractToken(urlWithKK)
	if token != "EM6wrnyAhBtOFvD7rdNu045xGpwb1bGD" {
		t.Errorf("Token extraído incorreto: %s", token)
	}

	rawToken := "EM6wrnyAhBtOFvD7rdNu045xGpwb1bGD"
	if ExtractToken(rawToken) != rawToken {
		t.Errorf("Token direto não foi reconhecido")
	}
}
