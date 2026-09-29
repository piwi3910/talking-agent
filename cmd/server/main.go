package main

import (
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/agent"
	"enterprise-ai-demo/internal/api"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/knowledge"
	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/memory"
	"enterprise-ai-demo/internal/mock"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/skills"
	"enterprise-ai-demo/internal/tools"
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
		mem = memory.NovaMemProvider{}
		return memory.ErrNotConfigured
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
			runtime.Clients[id] = &llm.OpenAI{BaseURL: os.Getenv("LLM_BASE_URL"), Model: os.Getenv("LLM_MODEL"), APIKeyEnv: env("LLM_API_KEY_ENV", "LLM_API_KEY"), Timeout: 60 * time.Second}
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
				if e = mem.Store(ctx, memory.Memory{Scope: memory.Scope{Tenant: a.Tenant, Organization: a.Organization, Domain: a.ID, Namespace: a.Memory.Namespace, User: seed.User}, Text: seed.Text, Tags: seed.Tags}); e != nil {
					return e
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
	app := &api.API{Runtime: runtime, Agents: agents, Sessions: session.NewStore(), BackendURL: backendURL, BackendURLs: backendURLs, Root: ctx, WebDir: web}
	server := &http.Server{Addr: env("LISTEN_ADDR", "127.0.0.1:8080"), Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("demo ready", "address", server.Addr, "llm", mode, "memory", mem.Name())
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
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
