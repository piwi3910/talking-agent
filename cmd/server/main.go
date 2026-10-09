package main

import (
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/agent"
	"enterprise-ai-demo/internal/api"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/knowledge"
	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/mcp"
	"enterprise-ai-demo/internal/memory"
	"enterprise-ai-demo/internal/mock"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/skills"
	"enterprise-ai-demo/internal/speech"
	"enterprise-ai-demo/internal/telephony"
	"enterprise-ai-demo/internal/tools"
	"enterprise-ai-demo/internal/voices"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func env(k, fallback string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return fallback
}
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	agents, err := config.Load(env("AGENTS_DIR", "agents"))
	if err != nil {
		return err
	}
	backendURL := os.Getenv("MOCK_BACKEND_URL")
	var mockServer *http.Server
	if backendURL == "" {
		b, e := mock.Load(env("MOCK_DATA_DIR", "mock"), time.Now().UTC())
		if e != nil {
			return e
		}
		b.LatencyScale = mock.LatencyScaleFromEnv()
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			return e
		}
		backendURL = "http://" + listener.Addr().String()
		mockServer = &http.Server{Handler: b.Handler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if e := mockServer.Serve(listener); e != nil && e != http.ErrServerClosed {
				slog.Error("mock server", "error", e)
				stop()
			}
		}()
		defer mockServer.Close()
	}
	var mem memory.Provider
	switch env("MEMORY_PROVIDER", "in-memory") {
	case "in-memory":
		mem = memory.New()
	case "novamem":
		credentials, e := memory.LoadNovaMemCredentials(os.Getenv("NOVAMEM_CREDENTIALS_FILE"))
		if e != nil {
			return e
		}
		var minScore *float64
		if raw := os.Getenv("NOVAMEM_MIN_VECTOR_SCORE"); raw != "" {
			score, parseErr := strconv.ParseFloat(raw, 64)
			if parseErr != nil {
				return fmt.Errorf("invalid NOVAMEM_MIN_VECTOR_SCORE")
			}
			minScore = &score
		}
		mem, e = memory.NewNovaMem(memory.NovaMemConfig{MinVectorScore: minScore, BaseURL: os.Getenv("NOVAMEM_BASE_URL"), Credentials: credentials, Timeout: 5 * time.Second})
		if e != nil {
			return e
		}
	default:
		return fmt.Errorf("unknown MEMORY_PROVIDER")
	}
	runtime := &agent.Runtime{Catalogs: map[string]skills.Catalog{}, Clients: map[string]llm.Client{}, Memory: mem, Knowledge: knowledge.Local{}, Tools: tools.HTTPExecutor{BaseURL: backendURL, Client: &http.Client{Timeout: 10 * time.Second}}, MaxIterations: 12}
	if raw := os.Getenv("MAX_ITERATIONS"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 30 {
			return fmt.Errorf("MAX_ITERATIONS must be 1–30")
		}
		runtime.MaxIterations = n
	}
	mcpDefaults, err := mcp.Defaults()
	if err != nil {
		slog.Warn("MCP defaults", "error", err)
	}
	mcpManager := &mcp.Manager{Registry: mcp.NewRegistry(env("MCP_SERVERS_FILE", "var/mcp-servers.json"), mcpDefaults)}
	defer mcpManager.Close()
	runtime.MCP = mcpManager
	backendURLs := map[string]string{}
	runtime.Executors = map[string]tools.Executor{}
	mode := env("LLM_PROVIDER", "demo")
	for id, a := range agents {
		endpoint := backendURL
		if a.Backend.URL != "" {
			endpoint = a.Backend.URL
		}
		if configured := os.Getenv(a.Backend.URLEnv); configured != "" {
			endpoint = configured
		}
		parsed, e := url.Parse(endpoint)
		if e != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("invalid backend URL for %s", id)
		}
		backendURLs[id] = endpoint
		runtime.Executors[id] = tools.HTTPExecutor{BaseURL: endpoint, Client: &http.Client{Timeout: 10 * time.Second}}

		catalog, e := skills.Load(a.Dir)
		if e != nil {
			return e
		}
		catalog, e = catalog.Select(a.Skills)
		if e != nil {
			return e
		}
		runtime.Catalogs[id] = catalog
		switch mode {
		case "demo":
			client, e := llm.LoadDemo(filepath.Join(a.Dir, "demo.json"))
			if e != nil {
				return e
			}
			runtime.Clients[id] = client
		case "openai-compatible":
			if os.Getenv("LLM_MODEL") == "" || os.Getenv("LLM_BASE_URL") == "" {
				return fmt.Errorf("set LLM_MODEL and LLM_BASE_URL for openai-compatible mode")
			}
			runtime.Clients[id] = &llm.OpenAI{BaseURL: os.Getenv("LLM_BASE_URL"), Model: os.Getenv("LLM_MODEL"), APIKeyEnv: env("LLM_API_KEY_ENV", "LLM_API_KEY"), Timeout: 60 * time.Second, DisableThinking: os.Getenv("LLM_DISABLE_THINKING") == "true"}
		default:
			return fmt.Errorf("unknown LLM_PROVIDER %s", mode)
		}
		raw, e := os.ReadFile(filepath.Join(a.Dir, "memory-seeds.json"))
		if e == nil {
			var seeds []struct {
				User string   `json:"user"`
				Text string   `json:"text"`
				Tags []string `json:"tags"`
			}
			if e = json.Unmarshal(raw, &seeds); e != nil {
				return e
			}
			for _, seed := range seeds {
				if e = mem.Store(ctx, memory.Memory{Scope: memory.Scope{Tenant: a.Tenant, Organization: a.Organization, Domain: a.MemoryDomain(), Namespace: a.Memory.Namespace, User: seed.User}, Text: seed.Text, Tags: seed.Tags}); e != nil {
					// Demo seeds are a convenience: a slow or unreachable memory
					// service must not keep the whole app from starting.
					slog.Warn("memory seeding skipped", "agent", id, "error", e)
					break
				}
			}
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	memctx, memcancel := context.WithCancel(context.Background())
	runtime.Start(memctx)
	defer func() { memcancel(); runtime.Wait() }()
	web := env("WEB_DIR", "web/dist")
	if _, err := os.Stat(web); err != nil {
		web = ""
	}
	ttsProvider := env("TTS_PROVIDER", speech.ProviderOmni)
	if ttsProvider != speech.ProviderQwen3 && ttsProvider != speech.ProviderOmni {
		return fmt.Errorf("unknown TTS_PROVIDER %q (use omni or qwen3)", ttsProvider)
	}
	if ttsProvider == speech.ProviderOmni && os.Getenv("TTS_URL") != "" && os.Getenv("TTS_RENDER_URL") == "" {
		return fmt.Errorf("TTS_PROVIDER omni needs TTS_RENDER_URL, the audio.cpp Qwen3-TTS worker that renders preset and designed voices")
	}
	speechClient := &speech.Client{STTURL: os.Getenv("STT_URL"), TTSURL: os.Getenv("TTS_URL"), Provider: ttsProvider, RenderURL: os.Getenv("TTS_RENDER_URL"), FinalURL: os.Getenv("STT_FINAL_URL"), FinalModel: os.Getenv("STT_FINAL_MODEL"), STTModel: os.Getenv("STT_MODEL"), STTLanguage: os.Getenv("STT_LANGUAGE")}
	voiceStore, err := voices.Open(env("VOICE_SETTINGS_FILE", "var/voice-settings.json"), env("VOICE_CUES_DIR", "var/voice-cues"), agents, speechClient)
	if err != nil {
		return fmt.Errorf("voice settings: %w", err)
	}
	voiceStore.Start(ctx)
	app := &api.API{MCP: mcpManager, Voices: voiceStore, Speech: speechClient, Runtime: runtime, Agents: agents, Sessions: session.NewStore(), BackendURL: backendURL, BackendURLs: backendURLs, Root: ctx, WebDir: web}
	if dir := env("TRACE_DIR", "var/traces"); dir != "" && dir != "off" {
		app.Traces = api.NewTraceStore(dir)
	}
	var sipCfg telephony.Config
	if path := os.Getenv("SIP_CONFIG_FILE"); path != "" {
		sipCfg, err = telephony.Load(path, agents)
		if err != nil {
			return fmt.Errorf("SIP configuration: %w", err)
		}
	}
	phoneSettings, err := telephony.OpenSettings(env("PHONE_SETTINGS_FILE", "var/phone-settings.json"), agents, sipCfg.Numbers)
	if err != nil {
		return fmt.Errorf("phone settings: %w", err)
	}
	app.PhoneSettings = phoneSettings
	gateway, err := telephony.OpenGateway(ctx, env("SIP_GATEWAY_FILE", "var/sip-gateway.json"), os.Getenv("SIP_ADVERTISE_IP"), &telephony.Server{Settings: phoneSettings, Agents: agents, Sessions: app.Sessions, Runtime: runtime, Speech: app.Speech, Voices: voiceStore})
	if err != nil {
		return fmt.Errorf("gateway settings: %w", err)
	}
	app.PhoneGateway = gateway
	defer gateway.Close()
	if gateway.Snapshot().Config.AutoConnect && os.Getenv("SIP_CONFIG_FILE") == "" {
		if err := gateway.Connect(); err != nil {
			slog.Warn("SIP reconnect failed", "error", err)
		}
	}
	var sipDone chan error
	if os.Getenv("SIP_CONFIG_FILE") != "" {
		phone := &telephony.Server{Config: sipCfg, Settings: phoneSettings, Agents: agents, Sessions: app.Sessions, Runtime: runtime, Speech: app.Speech, Voices: voiceStore}
		sipDone = make(chan error, 1)
		go func() { sipDone <- phone.Run(ctx) }()
		defer func() { stop(); <-sipDone }()
	}
	server := &http.Server{Addr: env("LISTEN_ADDR", "127.0.0.1:8080"), Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	defer server.Close()
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("demo ready", "address", server.Addr, "llm", mode, "memory", mem.Name())
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case e := <-sipDone:
		// Preserve the result for the shutdown join.
		sipDone <- e
		if e != nil {
			return e
		}
	case e := <-serverErrors:
		if e != http.ErrServerClosed {
			return e
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	app.Workers.Wait()
	return nil
}
