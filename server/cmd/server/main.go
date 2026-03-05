package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Cyber-Mat/obsidian-center/server/internal/api"
	"github.com/Cyber-Mat/obsidian-center/server/internal/auth"
	"github.com/Cyber-Mat/obsidian-center/server/internal/sync"
	"github.com/Cyber-Mat/obsidian-center/server/internal/vault"
)

type Config struct {
	Addr      string
	DBPath    string
	JWTSecret string
}

func main() {
	cfg := Config{}
	flag.StringVar(&cfg.Addr, "addr", ":8080", "server listen address")
	flag.StringVar(&cfg.DBPath, "db", "obsidian-center.db", "SQLite database path")
	flag.StringVar(&cfg.JWTSecret, "jwt-secret", "", "JWT signing secret (required)")
	flag.Parse()

	if cfg.JWTSecret == "" {
		cfg.JWTSecret = os.Getenv("OC_JWT_SECRET")
	}
	if cfg.JWTSecret == "" {
		fmt.Fprintln(os.Stderr, "error: jwt-secret is required (flag or OC_JWT_SECRET env)")
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	db, err := vault.OpenDB(cfg.DBPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := vault.Migrate(db); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	jwtService := auth.NewJWTService([]byte(cfg.JWTSecret))
	userStore := auth.NewUserStore(db)
	sessionStore := auth.NewSessionStore(db)
	vaultStore := vault.NewStore(db)

	// Initialize CRDT sync engine
	storeAdapter := sync.NewStoreAdapter(vaultStore)
	syncEngine := sync.NewEngine(storeAdapter)

	router := api.NewRouter(jwtService, userStore, sessionStore, vaultStore, syncEngine)

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("server starting", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	slog.Info("shutting down")

	// Persist all CRDT state before exit
	syncEngine.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}
