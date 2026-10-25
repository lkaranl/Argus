package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"argus/internal/config"
	"argus/internal/solar"
	"argus/internal/sse"
	"argus/internal/weather"
)

// ServerStatus detalha as métricas do próprio processo Argus
type ServerStatus struct {
	UptimeSeconds int64  `json:"uptime_seconds"`
	UptimeHuman   string `json:"uptime_human"`
	Version       string `json:"version"`
	CamerasTotal  int    `json:"cameras_total"`
}

// CombinedTelemetry consolida todo o estado que o HUD tático consome em tempo real
type CombinedTelemetry struct {
	Solar     *solar.SolarData     `json:"solar"`
	Weather   *weather.WeatherData `json:"weather"`
	Server    ServerStatus         `json:"server"`
	Timestamp int64                `json:"timestamp"`
}

// Manager coordena a coleta periódica e distribuição via SSE
type Manager struct {
	cfg         *config.Config
	solarClient *solar.Client
	weatherCli  *weather.Client
	broker      *sse.Broker

	startTime time.Time
	latest    *CombinedTelemetry
	mu        sync.RWMutex
}

// NewManager cria uma nova instância do gerenciador de telemetria
func NewManager(
	cfg *config.Config,
	solarClient *solar.Client,
	weatherCli *weather.Client,
	broker *sse.Broker,
) *Manager {
	return &Manager{
		cfg:         cfg,
		solarClient: solarClient,
		weatherCli:  weatherCli,
		broker:      broker,
		startTime:   time.Now(),
		latest: &CombinedTelemetry{
			Solar: &solar.SolarData{
				Available:     false,
				PowerRealtime: "0.00 kW",
				YieldToday:    "0.0 kWh",
			},
			Weather: &weather.WeatherData{
				Available:   false,
				CurrentTemp: 28,
				CurrentDesc: "Iniciando...",
				CurrentIcon: "🌤️",
				Days:        []weather.DayForecast{},
			},
			Server: ServerStatus{
				Version:      "1.0.0",
				CamerasTotal: len(cfg.Cameras),
			},
			Timestamp: time.Now().Unix(),
		},
	}
}

// Start dispara a coleta inicial imediata e os loops de ticker em background
func (m *Manager) Start(ctx context.Context) {
	// Ciclo inicial imediato
	m.refreshSolar(ctx)
	m.refreshWeather(ctx)
	m.broadcast()

	solarInterval := time.Duration(m.cfg.Solar.PollIntervalMinutes) * time.Minute
	if solarInterval < 1*time.Minute {
		solarInterval = 5 * time.Minute
	}

	weatherInterval := time.Duration(m.cfg.Weather.PollIntervalMinutes) * time.Minute
	if weatherInterval < 5*time.Minute {
		weatherInterval = 30 * time.Minute
	}

	solarTicker := time.NewTicker(solarInterval)
	weatherTicker := time.NewTicker(weatherInterval)
	uptimeTicker := time.NewTicker(30 * time.Second)

	defer func() {
		solarTicker.Stop()
		weatherTicker.Stop()
		uptimeTicker.Stop()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-solarTicker.C:
			m.refreshSolar(ctx)
			m.broadcast()
		case <-weatherTicker.C:
			m.refreshWeather(ctx)
			m.broadcast()
		case <-uptimeTicker.C:
			// Auto-recuperação rápida pós-queda de internet: se Solar ou Clima estiverem em modo fallback/offline,
			// tenta reconectar a cada 30s em vez de aguardar o ciclo longo de 5m/30m
			m.mu.RLock()
			solarNeedsRetry := m.cfg.Solar.Enabled && (m.latest.Solar == nil || !m.latest.Solar.Available || m.latest.Solar.IsFallback)
			weatherNeedsRetry := m.cfg.Weather.Enabled && (m.latest.Weather == nil || !m.latest.Weather.Available || m.latest.Weather.IsStale)
			m.mu.RUnlock()

			if solarNeedsRetry {
				m.refreshSolar(ctx)
			}
			if weatherNeedsRetry {
				m.refreshWeather(ctx)
			}

			m.updateServerStatus()
			m.broadcast()
		}
	}
}

// GetLatest retorna uma cópia segura dos últimos dados combinados
func (m *Manager) GetLatest() *CombinedTelemetry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.latest == nil {
		return nil
	}
	cpy := *m.latest
	return &cpy
}

func (m *Manager) refreshSolar(ctx context.Context) {
	data, err := m.solarClient.FetchData(ctx)
	if err == nil && data != nil {
		m.mu.Lock()
		m.latest.Solar = data
		m.latest.Timestamp = time.Now().Unix()
		m.mu.Unlock()
	}
}

func (m *Manager) refreshWeather(ctx context.Context) {
	data, err := m.weatherCli.FetchWeather(ctx)
	if err == nil && data != nil {
		m.mu.Lock()
		m.latest.Weather = data
		m.latest.Timestamp = time.Now().Unix()
		m.mu.Unlock()
	}
}

func (m *Manager) updateServerStatus() {
	uptimeSec := int64(time.Since(m.startTime).Seconds())
	m.mu.Lock()
	m.latest.Server.UptimeSeconds = uptimeSec
	m.latest.Server.UptimeHuman = formatUptime(uptimeSec)
	m.latest.Server.CamerasTotal = len(m.cfg.Cameras)
	m.latest.Timestamp = time.Now().Unix()
	m.mu.Unlock()
}

func (m *Manager) broadcast() {
	m.updateServerStatus()
	latest := m.GetLatest()
	if latest == nil || m.broker == nil {
		return
	}

	payload, err := json.Marshal(latest)
	if err == nil {
		m.broker.Broadcast("telemetry", payload)
	}
}

func formatUptime(sec int64) string {
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	s := sec % 60

	if d > 0 {
		return fmt.Sprintf("%dd %dh", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}
