package solar

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultHuaweiHost = "intl.fusionsolar.huawei.com"
	defaultKioskToken = "EM6wrnyAhBtOFvD7rdNu045xGpwb1bGD"
)

var (
	tokenRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-]{20,50}$`)
	kkRegex    = regexp.MustCompile(`kk=([a-zA-Z0-9_\-]+)`)
)

// Config armazena os parâmetros de conexão do quiosque Huawei FusionSolar
type Config struct {
	Enabled             bool
	Token               string
	KioskURL            string
	PollIntervalMinutes int
}

// DailyPoint representa um ponto amostrado da curva diária de geração solar
type DailyPoint struct {
	Time string  `json:"t"` // ex: "14:05"
	KW   float64 `json:"v"` // ex: 6.66
}

// SolarData representa as métricas estruturadas e prontas para o HUD
type SolarData struct {
	Available             bool         `json:"available"`
	StationName           string       `json:"station_name"`
	PowerRealtime         string       `json:"potencia_realtime"`
	PowerKW               float64      `json:"potencia_kw"`
	PeakPowerKW           float64      `json:"peak_power_kw,omitempty"`
	DailyCurve            []DailyPoint `json:"daily_curve,omitempty"`
	YieldToday            string       `json:"rendimento_hoje"`
	YieldTodayKWh         float64      `json:"rendimento_hoje_kwh"`
	YieldMonth            string       `json:"rendimento_mes"`
	GenerationYear        string       `json:"geracao_ano"`
	CumulativeElectricity string       `json:"eletricidade_acumulada"`
	CO2Reduction          string       `json:"co2_evitado"`
	TreesPlanted          string       `json:"arvores_plantadas"`
	Address               string       `json:"endereco"`
	DataHora              string       `json:"data_hora"`
	IsFallback            bool         `json:"is_fallback,omitempty"`
	Reason                string       `json:"reason,omitempty"`
	LastError             string       `json:"last_error,omitempty"`
	UpdatedAt             int64        `json:"updated_at"`
}

// Client gerencia a comunicação e cache de telemetria da Huawei FusionSolar
type Client struct {
	cfg        Config
	httpClient *http.Client
	cache      *SolarData
	cacheMu    sync.RWMutex
}

// NewClient cria uma nova instância do cliente Kiosk FusionSolar
func NewClient(cfg Config) *Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Transport: tr,
			Timeout:   8 * time.Second,
		},
	}
}

// ExtractToken extrai o token 'kk' a partir de uma string pura ou URL completa
func ExtractToken(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"'`)
	if raw == "" {
		return defaultKioskToken
	}

	// Caso 1: Token alfanumérico direto
	if tokenRegex.MatchString(raw) {
		return raw
	}

	// Caso 2: URL contendo parâmetro 'kk=...'
	if matches := kkRegex.FindStringSubmatch(raw); len(matches) > 1 {
		return matches[1]
	}

	return defaultKioskToken
}

// BuildRESTURL constrói a URL direta para a API REST da Huawei Kiosk
func (c *Client) BuildRESTURL() string {
	// Se a URL já for um endpoint REST direto (ex: em testes ou proxy)
	if strings.Contains(c.cfg.KioskURL, "/rest/pvms/web/kiosk/v1/station-kiosk-file") {
		return c.cfg.KioskURL
	}

	token := c.cfg.Token
	if token == "" {
		token = ExtractToken(c.cfg.KioskURL)
	}

	host := defaultHuaweiHost
	if c.cfg.KioskURL != "" {
		if parsed, err := url.Parse(c.cfg.KioskURL); err == nil && parsed.Host != "" {
			host = parsed.Host
		}
	}

	return fmt.Sprintf("https://%s/rest/pvms/web/kiosk/v1/station-kiosk-file?kk=%s", host, token)
}

// FetchData obtém a telemetria solar com cache e tratamento resiliente de erros
func (c *Client) FetchData(ctx context.Context) (*SolarData, error) {
	if !c.cfg.Enabled {
		return &SolarData{
			Available:     false,
			Reason:        "disabled",
			PowerRealtime: "0.00 kW",
			YieldToday:    "0.0 kWh",
		}, nil
	}

	targetURL := c.BuildRESTURL()
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return c.fallbackOrError(fmt.Sprintf("erro ao criar requisição HTTP: %v", err)), nil
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", "https://intl.fusionsolar.huawei.com/pvmswebsite/nologin/assets/build/cloud.html")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.fallbackOrError(fmt.Sprintf("falha de conexão com Huawei Cloud: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.fallbackOrError(fmt.Sprintf("Huawei retornou status HTTP %d", resp.StatusCode)), nil
	}

	var rawResponse struct {
		Success bool        `json:"success"`
		Data    interface{} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&rawResponse); err != nil {
		return c.fallbackOrError(fmt.Sprintf("falha ao decodificar JSON da Huawei: %v", err)), nil
	}

	if !rawResponse.Success || rawResponse.Data == nil {
		return c.fallbackOrError("resposta da Huawei não indicou sucesso"), nil
	}

	// O campo 'data' pode vir como string JSON codificada com HTML entities ou como map
	var stationJSON []byte
	switch v := rawResponse.Data.(type) {
	case string:
		stationJSON = []byte(html.UnescapeString(v))
	default:
		marshaled, err := json.Marshal(v)
		if err != nil {
			return c.fallbackOrError("erro ao processar campo 'data'"), nil
		}
		stationJSON = marshaled
	}

	var stationData struct {
		RealKPI struct {
			RealTimePower    float64 `json:"realTimePower"`
			DailyEnergy      float64 `json:"dailyEnergy"`
			MonthEnergy      float64 `json:"monthEnergy"`
			YearEnergy       float64 `json:"yearEnergy"`
			CumulativeEnergy float64 `json:"cumulativeEnergy"`
		} `json:"realKpi"`
		StationOverview struct {
			StationName  string `json:"stationName"`
			PlantAddress string `json:"plantAddress"`
		} `json:"stationOverview"`
		SocialContribution struct {
			CO2Reduction             float64 `json:"co2Reduction"`
			EquivalentTreePlanting   float64 `json:"equivalentTreePlanting"`
		} `json:"socialContribution"`
		PowerCurve struct {
			XAxis        []string `json:"xAxis"`
			CurrentPower string   `json:"currentPower"`
			ActivePower  []string `json:"activePower"`
		} `json:"powerCurve"`
	}

	if err := json.Unmarshal(stationJSON, &stationData); err != nil {
		return c.fallbackOrError(fmt.Sprintf("erro no parse interno dos dados da estação: %v", err)), nil
	}

	// Extração dos pontos da curva diária de geração solar (5 em 5 minutos)
	var dailyCurve []DailyPoint
	peakPower := 0.0

	for i := 0; i < len(stationData.PowerCurve.XAxis) && i < len(stationData.PowerCurve.ActivePower); i++ {
		rawP := stationData.PowerCurve.ActivePower[i]
		if rawP == "" || rawP == "-" {
			continue
		}
		var val float64
		if _, err := fmt.Sscanf(rawP, "%f", &val); err == nil && val >= 0 {
			dailyCurve = append(dailyCurve, DailyPoint{
				Time: stationData.PowerCurve.XAxis[i],
				KW:   val,
			})
			if val > peakPower {
				peakPower = val
			}
		}
	}

	// Formatações
	name := stationData.StationOverview.StationName
	if name == "" {
		name = "Usina Solar"
	}

	addr := stationData.StationOverview.PlantAddress
	if addr == "" {
		addr = "Brasil"
	}

	powerKW := stationData.RealKPI.RealTimePower
	if peakPower < powerKW {
		peakPower = powerKW
	}
	dailyKWh := stationData.RealKPI.DailyEnergy
	monthKWh := stationData.RealKPI.MonthEnergy
	yearKWh := stationData.RealKPI.YearEnergy
	cumKWh := stationData.RealKPI.CumulativeEnergy

	monthDisplay := fmt.Sprintf("%.1f kWh", monthKWh)
	if monthKWh >= 1000 {
		monthDisplay = fmt.Sprintf("%.2f MWh", monthKWh/1000.0)
	}

	yearDisplay := fmt.Sprintf("%.1f kWh", yearKWh)
	if yearKWh >= 1000 {
		yearDisplay = fmt.Sprintf("%.2f MWh", yearKWh/1000.0)
	}

	cumDisplay := fmt.Sprintf("%.1f kWh", cumKWh)
	if cumKWh >= 1000 {
		cumDisplay = fmt.Sprintf("%.2f MWh", cumKWh/1000.0)
	}

	co2Kg := stationData.SocialContribution.CO2Reduction
	co2Display := fmt.Sprintf("%.1f kg", co2Kg)
	if co2Kg >= 100 {
		co2Display = fmt.Sprintf("%.2f t", co2Kg/1000.0)
	}

	trees := int(stationData.SocialContribution.EquivalentTreePlanting)

	result := &SolarData{
		Available:             true,
		StationName:           name,
		PowerRealtime:         fmt.Sprintf("%.2f kW", powerKW),
		PowerKW:               powerKW,
		PeakPowerKW:           peakPower,
		DailyCurve:            dailyCurve,
		YieldToday:            fmt.Sprintf("%.1f kWh", dailyKWh),
		YieldTodayKWh:         dailyKWh,
		YieldMonth:            monthDisplay,
		GenerationYear:        yearDisplay,
		CumulativeElectricity: cumDisplay,
		CO2Reduction:          co2Display,
		TreesPlanted:          fmt.Sprintf("%d", trees),
		Address:               addr,
		DataHora:              time.Now().Format("02/01/2006 15:04"),
		UpdatedAt:             time.Now().Unix(),
	}

	c.cacheMu.Lock()
	c.cache = result
	c.cacheMu.Unlock()

	return result, nil
}

// fallbackOrError tenta recuperar snapshot local de dados_solar.json ou cache expirado
func (c *Client) fallbackOrError(errMsg string) *SolarData {
	// 1. Tenta carregar snapshot local de dados_solar.json
	candidateFiles := []string{
		"dados_solar.json",
		"/Users/karan/Github/Fusionsolar_kiosk/dados_solar.json",
	}

	for _, p := range candidateFiles {
		if raw, err := os.ReadFile(p); err == nil {
			var snap SolarData
			if err := json.Unmarshal(raw, &snap); err == nil && snap.PowerKW > 0 {
				snap.Available = true
				snap.IsFallback = true
				snap.LastError = errMsg
				snap.UpdatedAt = time.Now().Unix()
				return &snap
			}
		}
	}

	// 2. Cache anterior se disponível
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	if c.cache != nil {
		stale := *c.cache
		stale.IsFallback = true
		stale.LastError = errMsg
		return &stale
	}

	// 3. Fallback de desconexão seguro
	return &SolarData{
		Available:     false,
		Reason:        "huawei_unreachable",
		LastError:     errMsg,
		PowerRealtime: "0.00 kW",
		PowerKW:       0.0,
		YieldToday:    "0.0 kWh",
		YieldTodayKWh: 0.0,
		UpdatedAt:     time.Now().Unix(),
	}
}
