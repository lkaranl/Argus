package sse

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Broker gerencia clientes SSE e despacha mensagens em tempo real
type Broker struct {
	clients   map[chan []byte]bool
	clientsMu sync.RWMutex

	latestMsg []byte
	latestMu  sync.RWMutex
}

// NewBroker instancia um novo Broker SSE thread-safe
func NewBroker() *Broker {
	return &Broker{
		clients: make(map[chan []byte]bool),
	}
}

// ClientCount retorna o número de conexões ativas
func (b *Broker) ClientCount() int {
	b.clientsMu.RLock()
	defer b.clientsMu.RUnlock()
	return len(b.clients)
}

// Broadcast formata e envia um evento SSE para todos os clientes conectados
func (b *Broker) Broadcast(event string, data []byte) {
	// Formato padrão SSE: event: {event}\ndata: {data}\n\n
	msg := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(data)))

	// Salva última mensagem para novos clientes
	b.latestMu.Lock()
	b.latestMsg = msg
	b.latestMu.Unlock()

	b.clientsMu.RLock()
	defer b.clientsMu.RUnlock()

	for clientChan := range b.clients {
		select {
		case clientChan <- msg:
		default:
			// Canal cheio (cliente lento ou travado) - descarta para evitar travamento geral
		}
	}
}

// ServeHTTP lida com conexões HTTP persistentes para o stream SSE
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming SSE não suportado pelo servidor", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Cria canal com buffer para receber eventos
	clientChan := make(chan []byte, 32)

	b.clientsMu.Lock()
	b.clients[clientChan] = true
	b.clientsMu.Unlock()

	defer func() {
		b.clientsMu.Lock()
		delete(b.clients, clientChan)
		b.clientsMu.Unlock()
		close(clientChan)
	}()

	// Envia imediatamente o último estado conhecido se disponível
	b.latestMu.RLock()
	if len(b.latestMsg) > 0 {
		_, _ = w.Write(b.latestMsg)
		flusher.Flush()
	}
	b.latestMu.RUnlock()

	// Ticker de heartbeat (ping) a cada 15 segundos
	pingTicker := time.NewTicker(15 * time.Second)
	defer pingTicker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-clientChan:
			if !ok {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		case <-pingTicker.C:
			// Comentário de heartbeat SSE para manter o socket TCP vivo
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
