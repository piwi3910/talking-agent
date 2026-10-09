package main

import (
	"context"
	"enterprise-ai-demo/internal/mock"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	root := os.Getenv("MOCK_DATA_DIR")
	if root == "" {
		root = "mock"
	}
	b, e := mock.Load(root, time.Now().UTC())
	if e != nil {
		slog.Error("load mock data", "error", e)
		os.Exit(1)
	}
	b.LatencyScale = mock.LatencyScaleFromEnv()
	addr := os.Getenv("MOCK_LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8081"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: addr, Handler: b.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("mock backend ready", "address", addr)
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		slog.Error("serve", "error", e)
		os.Exit(1)
	}
}
