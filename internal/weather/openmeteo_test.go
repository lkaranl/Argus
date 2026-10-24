package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenMeteoClient(t *testing.T) {
	mockResponse := `{
		"current_weather": {
			"temperature": 32.4,
			"weathercode": 1,
			"is_day": 1
		},
		"daily": {
			"time": ["2026-10-24", "2026-10-25", "2026-10-26"],
			"temperature_2m_max": [35.2, 36.0, 34.1],
			"temperature_2m_min": [24.0, 23.5, 23.0],
			"shortwave_radiation_sum": [23.4, 21.6, 14.4],
			"weathercode": [0, 1, 80]
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	client := NewClient(Config{
		Enabled:   true,
		Latitude:  -10.8778,
		Longitude: -61.9514,
		CityName:  "Ji-Paraná, RO",
		BaseURL:   server.URL,
	})

	weather, err := client.FetchWeather(context.Background())
	if err != nil {
		t.Fatalf("Erro ao consultar clima: %v", err)
	}

	if !weather.Available {
		t.Fatal("Esperava previsão disponível (Available = true)")
	}

	if weather.CurrentTemp != 32 {
		t.Errorf("Temperatura atual esperada 32, obteve %d", weather.CurrentTemp)
	}

	if weather.CurrentDesc != "Céu Aberto" {
		t.Errorf("Descrição esperada 'Céu Aberto', obteve '%s'", weather.CurrentDesc)
	}

	if len(weather.Days) != 3 {
		t.Fatalf("Esperava 3 dias de previsão, obteve %d", len(weather.Days))
	}

	// Teste do dia 0 (23.4 MJ / 3.6 = 6.5 kWh/m² -> Excelente)
	day0 := weather.Days[0]
	if day0.SolarKWhM2 != 6.5 {
		t.Errorf("Esperava 6.5 kWh/m², obteve %f", day0.SolarKWhM2)
	}
	if day0.SolarPotential != "Excelente" {
		t.Errorf("Esperava potencial 'Excelente', obteve '%s'", day0.SolarPotential)
	}

	// Teste do dia 2 (14.4 MJ / 3.6 = 4.0 kWh/m² -> Moderado)
	day2 := weather.Days[2]
	if day2.SolarKWhM2 != 4.0 {
		t.Errorf("Esperava 4.0 kWh/m², obteve %f", day2.SolarKWhM2)
	}
	if day2.SolarPotential != "Moderado" {
		t.Errorf("Esperava potencial 'Moderado', obteve '%s'", day2.SolarPotential)
	}
}
