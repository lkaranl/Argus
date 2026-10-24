package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// ServerConfig configurações de rede do servidor HTTP
type ServerConfig struct {
	Port        int    `yaml:"port"`
	BindAddress string `yaml:"bind_address"`
}

// CameraConfig define os parâmetros de cada câmera IP monitorada
type CameraConfig struct {
	ID      string `yaml:"id"`
	Name    string `yaml:"name"`
	RTSPURL string `yaml:"rtsp_url"`
}

// SolarConfig define os parâmetros para coleta de telemetria fotovoltaica
type SolarConfig struct {
	Enabled             bool   `yaml:"enabled"`
	Token               string `yaml:"token"`
	KioskURL            string `yaml:"kiosk_url"`
	PollIntervalMinutes int    `yaml:"poll_interval_minutes"`
}

// WeatherConfig define os parâmetros para consulta climática na Open-Meteo
type WeatherConfig struct {
	Enabled             bool    `yaml:"enabled"`
	Latitude            float64 `yaml:"latitude"`
	Longitude           float64 `yaml:"longitude"`
	CityName            string  `yaml:"city_name"`
	PollIntervalMinutes int     `yaml:"poll_interval_minutes"`
}

// Config representa a estrutura unificada de configuração do Argus
type Config struct {
	Server  ServerConfig   `yaml:"server"`
	Cameras []CameraConfig `yaml:"cameras"`
	Solar   SolarConfig    `yaml:"solar"`
	Weather WeatherConfig  `yaml:"weather"`
}

// LoadConfig carrega o arquivo .env (se existir) e parseia o YAML com interpolação de variáveis
func LoadConfig(path string) (*Config, error) {
	// Carrega .env silenciosamente se existir
	_ = godotenv.Load()

	// Lê o arquivo de configuração
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("não foi possível ler o arquivo de configuração em %s: %w", path, err)
	}

	// Interpola variáveis de ambiente no formato ${VAR_NAME}
	expanded := os.ExpandEnv(string(raw))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("erro ao decodificar yaml de configuração: %w", err)
	}

	// Valores padrão
	if cfg.Server.Port <= 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.BindAddress == "" {
		cfg.Server.BindAddress = "0.0.0.0"
	}
	if cfg.Solar.PollIntervalMinutes <= 0 {
		cfg.Solar.PollIntervalMinutes = 5
	}
	if cfg.Weather.PollIntervalMinutes <= 0 {
		cfg.Weather.PollIntervalMinutes = 30
	}

	// Sobrescritas por variáveis de ambiente
	if portStr := os.Getenv("ARGUS_PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			cfg.Server.Port = p
		}
	}
	if bind := os.Getenv("ARGUS_BIND_ADDRESS"); bind != "" {
		cfg.Server.BindAddress = bind
	}

	// Solar envs
	if token := os.Getenv("FUSIONSOLAR_TOKEN"); token != "" {
		cfg.Solar.Token = token
	}
	if kioskURL := os.Getenv("FUSIONSOLAR_KIOSK_URL"); kioskURL != "" {
		cfg.Solar.KioskURL = kioskURL
	}
	if enabledStr := os.Getenv("FUSIONSOLAR_ENABLED"); enabledStr != "" {
		cfg.Solar.Enabled = enabledStr == "true" || enabledStr == "1"
	} else if cfg.Solar.Token != "" || cfg.Solar.KioskURL != "" {
		cfg.Solar.Enabled = true
	}

	// Weather envs
	if city := os.Getenv("WEATHER_CITY"); city != "" {
		cfg.Weather.CityName = city
	}
	if latStr := os.Getenv("WEATHER_LAT"); latStr != "" {
		if lat, err := strconv.ParseFloat(latStr, 64); err == nil {
			cfg.Weather.Latitude = lat
		}
	}
	if lonStr := os.Getenv("WEATHER_LON"); lonStr != "" {
		if lon, err := strconv.ParseFloat(lonStr, 64); err == nil {
			cfg.Weather.Longitude = lon
		}
	}

	return &cfg, nil
}
