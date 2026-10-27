package solar

import (
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryStore(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "solar_history.json")

	store := NewHistoryStore(storePath)

	now := time.Now()
	todayStr := now.Format("2006-01-02")
	yesterdayStr := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Grava yield de ontem e de hoje
	store.RecordDailyYield(yesterdayStr, 45.7)
	store.RecordDailyYield(todayStr, 8.8)

	slots := store.GetSlots(7, 8.8)
	if len(slots) != 7 {
		t.Fatalf("Esperava 7 slots, obteve %d", len(slots))
	}

	// O último slot deve ser hoje
	todaySlot := slots[len(slots)-1]
	if !todaySlot.IsToday {
		t.Errorf("Esperava que o último slot fosse marcado como hoje")
	}
	if todaySlot.Date != todayStr {
		t.Errorf("Data de hoje incorreta: %s != %s", todaySlot.Date, todayStr)
	}
	if todaySlot.KWh == nil || *todaySlot.KWh != 8.8 {
		t.Errorf("Valor de hoje incorreto: %v", todaySlot.KWh)
	}

	// O penúltimo slot deve ser ontem com 45.7 kWh
	yesterdaySlot := slots[len(slots)-2]
	if yesterdaySlot.KWh == nil || *yesterdaySlot.KWh != 45.7 {
		t.Errorf("Valor de ontem incorreto: %v", yesterdaySlot.KWh)
	}

	// Testa recarga do arquivo
	store2 := NewHistoryStore(storePath)
	slots2 := store2.GetSlots(7, 8.8)
	if slots2[len(slots2)-2].KWh == nil || *slots2[len(slots2)-2].KWh != 45.7 {
		t.Errorf("Falha ao persistir e recarregar histórico do arquivo")
	}
}
