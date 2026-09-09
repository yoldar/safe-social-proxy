package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yoldar/safe-social-proxy/internal/config"
	"github.com/yoldar/safe-social-proxy/internal/proxy"
	"github.com/yoldar/safe-social-proxy/internal/server"
	"github.com/yoldar/safe-social-proxy/internal/youtube"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}
	reg, err := proxy.NewRegistry(cfg, logger)
	if err != nil {
		logger.Error("registry", "err", err)
		os.Exit(1)
	}
	srv, err := server.New(cfg, reg, youtube.New(), logger)
	if err != nil {
		logger.Error("server", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       90 * time.Second,
		// No WriteTimeout: long-lived video streams must not be cut off.
	}

	go func() {
		logger.Info("listening",
			"addr", cfg.Listen, "portal", cfg.PublicURL(""), "sites", len(reg.Sites))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
