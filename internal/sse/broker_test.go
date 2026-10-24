package sse

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSEBroker(t *testing.T) {
	broker := NewBroker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cria requisição fake para o endpoint SSE
	req := httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		broker.ServeHTTP(rec, req)
		close(done)
	}()

	// Aguarda o cliente ser registrado no broker
	time.Sleep(50 * time.Millisecond)
	if count := broker.ClientCount(); count != 1 {
		t.Fatalf("Esperava 1 cliente conectado, obteve %d", count)
	}

	// Envia mensagem via broadcast
	testPayload := `{"status":"ok","power":4.5}`
	broker.Broadcast("telemetry", []byte(testPayload))

	// Aguarda processamento da mensagem
	time.Sleep(50 * time.Millisecond)

	// Cancela a conexão do cliente
	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout aguardando desconexão do cliente SSE")
	}

	// Verifica se o cliente foi removido após fechar a conexão
	if count := broker.ClientCount(); count != 0 {
		t.Errorf("Esperava 0 clientes após desconexão, obteve %d", count)
	}

	// Verifica o output SSE
	body := rec.Body.String()
	if !strings.Contains(body, "event: telemetry") {
		t.Errorf("Resposta não contém 'event: telemetry'. Resposta: %s", body)
	}
	if !strings.Contains(body, testPayload) {
		t.Errorf("Resposta não contém o payload esperado. Resposta: %s", body)
	}
}

func TestSSEBrokerSlowClientDrop(t *testing.T) {
	broker := NewBroker()

	// Simula cliente com canal cheio para testar descarte sem travamento
	clientChan := make(chan []byte, 1)
	clientChan <- []byte("antigo") // Enche o buffer

	broker.clientsMu.Lock()
	broker.clients[clientChan] = true
	broker.clientsMu.Unlock()

	// Broadcast não deve travar nem causar deadlock
	broker.Broadcast("test", []byte("novo"))

	broker.clientsMu.Lock()
	delete(broker.clients, clientChan)
	broker.clientsMu.Unlock()
}
