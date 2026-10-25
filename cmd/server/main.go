package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"argus/internal/config"
	"argus/internal/solar"
	"argus/internal/sse"
	"argus/internal/stream"
	"argus/internal/telemetry"
	"argus/internal/weather"
	"argus/web"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func setupRouter(
	cfg *config.Config,
	broker *sse.Broker,
	telemetryMgr *telemetry.Manager,
	streamEngine *stream.Engine,
) http.Handler {
	r := chi.NewRouter()

	// Middlewares essenciais e de baixo consumo de recursos
	r.Use(middleware.Recoverer)

	// Sub-rotas da API WHEP e Telemetria
	r.Route("/api", func(api chi.Router) {
		// Negociação WHEP para câmeras WebRTC
		api.HandleFunc("/whep/{camera_id}", func(w http.ResponseWriter, r *http.Request) {
			camID := chi.URLParam(r, "camera_id")
			streamEngine.ServeWHEP(w, r, camID)
		})

		api.HandleFunc("/whep/{camera_id}/session/{session_id}", func(w http.ResponseWriter, r *http.Request) {
			camID := chi.URLParam(r, "camera_id")
			streamEngine.ServeWHEP(w, r, camID)
		})

		// Canal SSE persistente em tempo real
		api.Get("/events", func(w http.ResponseWriter, r *http.Request) {
			broker.ServeHTTP(w, r)
		})

		// Healthcheck e diagnóstico
		api.Get("/status", func(w http.ResponseWriter, r *http.Request) {
			latest := telemetryMgr.GetLatest()
			resp := map[string]interface{}{
				"status":    "online",
				"version":   "1.0.0",
				"telemetry": latest,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		})

		// Lista de câmeras
		api.Get("/cameras", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(streamEngine.ListCameras())
		})

		// Diagnóstico completo do sistema e streams
		api.Get("/diagnostics", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":    "online",
				"time":      time.Now().Format(time.RFC3339),
				"cameras":   streamEngine.ListCameras(),
				"telemetry": telemetryMgr.GetLatest(),
			})
		})
	})

	// Servidor de arquivos estáticos embutidos via //go:embed
	staticFS, err := web.StaticFS()
	if err != nil {
		log.Fatalf("[Argus] Falha crítica ao carregar arquivos estáticos embutidos: %v", err)
	}

	r.Handle("/*", http.FileServer(http.FS(staticFS)))

	return r
}

func main() {
	configPath := flag.String("config", "config.yaml", "Caminho para o arquivo de configuração config.yaml")
	flag.Parse()

	log.Println("==================================================================")
	log.Println("🛡️  ARGUS — CCTV Command Hub (Iniciando...)")
	log.Println("==================================================================")

	// Carrega configuração
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Printf("[Argus] Arquivo '%s' não encontrado. Tentando 'config.example.yaml'...", *configPath)
		cfg, err = config.LoadConfig("config.example.yaml")
		if err != nil {
			log.Fatalf("[Argus] Falha fatal ao carregar configuração: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Inicializa SSE Broker
	broker := sse.NewBroker()

	// 2. Inicializa Clientes Externos
	solarClient := solar.NewClient(solar.Config{
		Enabled:             cfg.Solar.Enabled,
		Token:               cfg.Solar.Token,
		KioskURL:            cfg.Solar.KioskURL,
		PollIntervalMinutes: cfg.Solar.PollIntervalMinutes,
	})

	weatherClient := weather.NewClient(weather.Config{
		Enabled:             cfg.Weather.Enabled,
		Latitude:            cfg.Weather.Latitude,
		Longitude:           cfg.Weather.Longitude,
		CityName:            cfg.Weather.CityName,
		PollIntervalMinutes: cfg.Weather.PollIntervalMinutes,
	})

	// 3. Orquestrador de Telemetria
	telemetryMgr := telemetry.NewManager(cfg, solarClient, weatherClient, broker)
	go telemetryMgr.Start(ctx)

	// 4. Motor de Streaming WebRTC WHEP
	streamEngine := stream.NewEngine(cfg.Cameras)
	defer streamEngine.Close()

	// 5. Roteador HTTP
	router := setupRouter(cfg, broker, telemetryMgr, streamEngine)

	addr := fmt.Sprintf("%s:%d", cfg.Server.BindAddress, cfg.Server.Port)
	server := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	// Servidor HTTP em goroutine separada
	go func() {
		log.Printf("[Argus] Servidor pronto e operando em http://%s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Argus] Falha no servidor HTTP: %v", err)
		}
	}()

	// Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[Argus] Encerrando servidor graciosamente...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("[Argus] Erro ao desligar servidor HTTP: %v", err)
	}

	log.Println("[Argus] Processo finalizado com sucesso.")
}
