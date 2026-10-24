package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"
)

// Config define os parâmetros de consulta e geolocalização do Open-Meteo
type Config struct {
	Enabled             bool
	Latitude            float64
	Longitude           float64
	CityName            string
	PollIntervalMinutes int
	BaseURL             string // Usado principalmente para testes mockados
}

// DayForecast armazena a previsão diária combinada com métricas fotovoltaicas
type DayForecast struct {
	Date             string  `json:"date"`
	DayIndex         int     `json:"day_index"`
	DayLabel         string  `json:"day_label"`
	TempMax          int     `json:"temp_max"`
	TempMin          int     `json:"temp_min"`
	RadiationMJ      float64 `json:"radiation_mj"`
	SolarKWhM2       float64 `json:"solar_kwh_m2"`
	EstGenerationKWh float64 `json:"est_generation_kwh"`
	SolarPotential   string  `json:"solar_potential"`
	SolarColor       string  `json:"solar_color"`
	WeatherCode      int     `json:"weather_code"`
	WeatherDesc      string  `json:"weather_desc"`
	WeatherIcon      string  `json:"weather_icon"`
}

// WeatherData agrupa o clima atual e a previsão dos próximos dias
type WeatherData struct {
	Available   bool          `json:"available"`
	City        string        `json:"city"`
	CurrentTemp int           `json:"current_temp"`
	CurrentCode int           `json:"current_code"`
	CurrentDesc string        `json:"current_desc"`
	CurrentIcon string        `json:"current_icon"`
	IsDay       int           `json:"is_day"`
	Days        []DayForecast `json:"days"`
	CachedAt    int64         `json:"cached_at"`
	IsStale     bool          `json:"is_stale,omitempty"`
}

// Client gerencia a consulta e cache em memória da API Open-Meteo
type Client struct {
	cfg        Config
	httpClient *http.Client
	cache      *WeatherData
	cacheMu    sync.RWMutex
}

// NewClient cria uma nova instância do cliente Open-Meteo
func NewClient(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.open-meteo.com"
	}
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Mapeamento de códigos meteorológicos WMO para legendas pt-BR e ícones
func mapWMO(code int) (string, string) {
	switch code {
	case 0:
		return "Ensolarado", "☀️"
	case 1:
		return "Céu Aberto", "🌤️"
	case 2:
		return "Parcialmente Nublado", "⛅"
	case 3:
		return "Nublado", "☁️"
	case 45, 48:
		return "Nevoeiro", "🌫️"
	case 51, 53, 55:
		return "Garoa", "🌧️"
	case 61, 63:
		return "Chuva Leve", "🌧️"
	case 65:
		return "Chuva Forte", "🌧️"
	case 80, 81:
		return "Pancadas de Chuva", "🌦️"
	case 82:
		return "Chuva Torrencial", "⛈️"
	case 95, 96, 99:
		return "Tempestade", "⛈️"
	default:
		return "Céu Aberto", "🌤️"
	}
}

// FetchWeather busca a previsão na Open-Meteo ou retorna cache válido
func (c *Client) FetchWeather(ctx context.Context) (*WeatherData, error) {
	if !c.cfg.Enabled {
		return &WeatherData{
			Available:   false,
			City:        c.cfg.CityName,
			CurrentTemp: 25,
			CurrentDesc: "Desativado",
			CurrentIcon: "🌤️",
			Days:        []DayForecast{},
			CachedAt:    time.Now().Unix(),
		}, nil
	}

	// 1. Verifica cache em memória válido (30 minutos = 1800s)
	now := time.Now().Unix()
	c.cacheMu.RLock()
	if c.cache != nil && (now-c.cache.CachedAt < 1800) {
		cached := *c.cache
		c.cacheMu.RUnlock()
		return &cached, nil
	}
	c.cacheMu.RUnlock()

	// 2. Monta a URL de consulta
	queryURL := fmt.Sprintf(
		"%s/v1/forecast?latitude=%.4f&longitude=%.4f&daily=temperature_2m_max,temperature_2m_min,shortwave_radiation_sum,weathercode&current_weather=true&timezone=auto",
		c.cfg.BaseURL, c.cfg.Latitude, c.cfg.Longitude,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return c.fallbackCache(now), nil
	}
	req.Header.Set("User-Agent", "Argus-CCTV-Weather/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.fallbackCache(now), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.fallbackCache(now), nil
	}

	var raw struct {
		CurrentWeather struct {
			Temperature float64 `json:"temperature"`
			WeatherCode int     `json:"weathercode"`
			IsDay       int     `json:"is_day"`
		} `json:"current_weather"`
		Daily struct {
			Time                  []string  `json:"time"`
			Temperature2mMax      []float64 `json:"temperature_2m_max"`
			Temperature2mMin      []float64 `json:"temperature_2m_min"`
			ShortwaveRadiationSum []float64 `json:"shortwave_radiation_sum"`
			WeatherCode           []int     `json:"weathercode"`
		} `json:"daily"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return c.fallbackCache(now), nil
	}

	dayLabels := []string{"Hoje", "Amanhã", "Depois"}
	daysCount := len(raw.Daily.Time)
	if daysCount > 3 {
		daysCount = 3
	}

	days := make([]DayForecast, 0, daysCount)
	for i := 0; i < daysCount; i++ {
		mj := 21.0
		if i < len(raw.Daily.ShortwaveRadiationSum) {
			mj = raw.Daily.ShortwaveRadiationSum[i]
		}

		// Conversão física: 1 MJ/m² = 1 / 3.6 kWh/m²
		kwhM2 := math.Round((mj/3.6)*100) / 100
		estGen := math.Round((kwhM2*0.20)*100) / 100

		var potential, color string
		if kwhM2 >= 6.0 {
			potential = "Excelente"
			color = "#FFB300"
		} else if kwhM2 >= 4.5 {
			potential = "Bom"
			color = "#FFA000"
		} else if kwhM2 >= 3.0 {
			potential = "Moderado"
			color = "#FB8C00"
		} else {
			potential = "Baixo"
			color = "#90A4AE"
		}

		wcode := 0
		if i < len(raw.Daily.WeatherCode) {
			wcode = raw.Daily.WeatherCode[i]
		}
		desc, icon := mapWMO(wcode)

		label := dayLabels[i]
		maxTemp := 32
		if i < len(raw.Daily.Temperature2mMax) {
			maxTemp = int(math.Round(raw.Daily.Temperature2mMax[i]))
		}
		minTemp := 22
		if i < len(raw.Daily.Temperature2mMin) {
			minTemp = int(math.Round(raw.Daily.Temperature2mMin[i]))
		}

		days = append(days, DayForecast{
			Date:             raw.Daily.Time[i],
			DayIndex:         i,
			DayLabel:         label,
			TempMax:          maxTemp,
			TempMin:          minTemp,
			RadiationMJ:      math.Round(mj*10) / 10,
			SolarKWhM2:       kwhM2,
			EstGenerationKWh: estGen,
			SolarPotential:   potential,
			SolarColor:       color,
			WeatherCode:      wcode,
			WeatherDesc:      desc,
			WeatherIcon:      icon,
		})
	}

	cDesc, cIcon := mapWMO(raw.CurrentWeather.WeatherCode)
	city := c.cfg.CityName
	if city == "" {
		city = "Ji-Paraná, RO"
	}

	result := &WeatherData{
		Available:   true,
		City:        city,
		CurrentTemp: int(math.Round(raw.CurrentWeather.Temperature)),
		CurrentCode: raw.CurrentWeather.WeatherCode,
		CurrentDesc: cDesc,
		CurrentIcon: cIcon,
		IsDay:       raw.CurrentWeather.IsDay,
		Days:        days,
		CachedAt:    now,
	}

	c.cacheMu.Lock()
	c.cache = result
	c.cacheMu.Unlock()

	return result, nil
}

// fallbackCache retorna cache anterior ou mock estático seguro se a API falhar
func (c *Client) fallbackCache(now int64) *WeatherData {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	if c.cache != nil {
		stale := *c.cache
		stale.IsStale = true
		return &stale
	}

	return &WeatherData{
		Available:   false,
		City:        c.cfg.CityName,
		CurrentTemp: 30,
		CurrentDesc: "Céu Aberto",
		CurrentIcon: "🌤️",
		IsDay:       1,
		Days: []DayForecast{
			{DayLabel: "Hoje", TempMax: 35, TempMin: 24, WeatherDesc: "Céu Aberto", WeatherIcon: "🌤️", SolarPotential: "Bom", SolarColor: "#FFA000"},
			{DayLabel: "Amanhã", TempMax: 36, TempMin: 23, WeatherDesc: "Céu Aberto", WeatherIcon: "🌤️", SolarPotential: "Bom", SolarColor: "#FFA000"},
			{DayLabel: "Depois", TempMax: 34, TempMin: 23, WeatherDesc: "Pancadas", WeatherIcon: "🌦️", SolarPotential: "Moderado", SolarColor: "#FB8C00"},
		},
		CachedAt: now,
		IsStale:  true,
	}
}
