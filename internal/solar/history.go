package solar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DailySlot representa a geração solar de um dia para gráficos do dashboard
type DailySlot struct {
	Date    string   `json:"date"`     // YYYY-MM-DD
	Weekday int      `json:"weekday"`  // 0 = Segunda-feira ... 6 = Domingo
	KWh     *float64 `json:"kwh"`      // Valor em kWh ou null se não houver registro
	IsToday bool     `json:"is_today"` // True se for a data atual
}

// HistoryStore gerencia e persiste os registros diários de geração solar em arquivo JSON leve
type HistoryStore struct {
	filePath string
	mu       sync.RWMutex
	records  map[string]float64
}

// NewHistoryStore inicializa o repositório de histórico
func NewHistoryStore(filePath string) *HistoryStore {
	if filePath == "" {
		filePath = filepath.Join("data", "solar_history.json")
	}

	store := &HistoryStore{
		filePath: filePath,
		records:  make(map[string]float64),
	}
	store.load()
	return store
}

func (s *HistoryStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}

	var loaded map[string]float64
	if err := json.Unmarshal(data, &loaded); err == nil && loaded != nil {
		s.records = loaded
	}
}

func (s *HistoryStore) save() {
	if s.filePath == "" {
		return
	}

	dir := filepath.Dir(s.filePath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}

	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return
	}

	_ = os.WriteFile(s.filePath, data, 0644)
}

// RecordDailyYield registra o rendimento acumulado de um dia (YYYY-MM-DD)
func (s *HistoryStore) RecordDailyYield(date string, kwh float64) {
	if date == "" || kwh <= 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Só atualiza se o novo valor for maior ou se não existir registro para o dia
	existing, ok := s.records[date]
	if !ok || kwh >= existing {
		s.records[date] = kwh
		s.save()
	}
}

// GetSlots retorna os últimos N dias contínuos terminando hoje
func (s *HistoryStore) GetSlots(days int, todayYieldKWh float64) []DailySlot {
	if days <= 0 {
		days = 7
	}
	if days > 31 {
		days = 31
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	todayStr := now.Format("2006-01-02")
	slots := make([]DailySlot, days)

	for i := days - 1; i >= 0; i-- {
		idx := days - 1 - i
		d := now.AddDate(0, 0, -i)
		dateStr := d.Format("2006-01-02")
		// 0 = Segunda ... 6 = Domingo
		weekday := (int(d.Weekday()) + 6) % 7

		isToday := (dateStr == todayStr)
		var valPtr *float64

		if isToday {
			if todayYieldKWh > 0 {
				v := todayYieldKWh
				valPtr = &v
			} else if stored, ok := s.records[dateStr]; ok {
				v := stored
				valPtr = &v
			}
		} else {
			if stored, ok := s.records[dateStr]; ok {
				v := stored
				valPtr = &v
			}
		}

		slots[idx] = DailySlot{
			Date:    dateStr,
			Weekday: weekday,
			KWh:     valPtr,
			IsToday: isToday,
		}
	}

	return slots
}
