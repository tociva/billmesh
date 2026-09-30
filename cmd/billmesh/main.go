package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/tociva/billmesh/internal/app"
	"github.com/tociva/billmesh/internal/auth"
	"github.com/tociva/billmesh/internal/bff"
	"github.com/tociva/billmesh/internal/config"
	"github.com/tociva/billmesh/internal/database"
)

func main() {
	if err := run(); err != nil {
		slog.Error("billmesh stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: billmesh api|worker|migrate|healthcheck")
	}
	if os.Args[1] == "healthcheck" {
		if len(os.Args) != 3 {
			return errors.New("usage: billmesh healthcheck URL")
		}
		return healthcheck(os.Args[2])
	}
	if err := loadEnvironment(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Args[1] == "migrate" {
		command := "up"
		if len(os.Args) > 2 {
			command = os.Args[2]
		}
		return database.Migrate(ctx, cfg.DatabaseURL, command)
	}
	if os.Args[1] == "api" {
		if err := validateAPIAuthConfig(cfg); err != nil {
			return err
		}
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	switch os.Args[1] {
	case "api":
		verifier := auth.NewJWKSVerifier(cfg.OIDCIssuer, cfg.OIDCAudience, nil)
		api := app.NewAPI(pool, verifier, nil)
		api.ConfigureOpenAPI(cfg.PublicOpenAPI)
		managers := make([]*bff.Manager, 0, len(cfg.BFF.Realms))
		for _, realm := range cfg.BFF.Realms {
			browserVerifier := auth.NewJWKSVerifier(realm.Issuer, realm.Audience, nil)
			manager, err := bff.New(realm, pool, browserVerifier, nil)
			if err != nil {
				return fmt.Errorf("configure %s BFF: %w", realm.Realm, err)
			}
			managers = append(managers, manager)
		}
		browser, err := bff.NewRouter(managers...)
		if err != nil {
			return fmt.Errorf("configure BFF router: %w", err)
		}
		api.ConfigureBrowserAuth(browser)
		api.ConfigureRequestLimits(cfg.AuthFailuresPerMinute, cfg.MutationRatePerSecond, cfg.MutationBurst)
		server := newAPIServer(cfg.HTTPAddr, api.Handler())
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()
		slog.Info("API listening", "address", cfg.HTTPAddr)
		err = server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case "worker":
		return app.NewWorker(pool, nil, cfg.WorkerInterval, nil).Run(ctx)
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}

func loadEnvironment(filenames ...string) error {
	if err := godotenv.Load(filenames...); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}
	return nil
}

func validateAPIAuthConfig(cfg config.Config) error {
	if cfg.OIDCIssuer == "" || cfg.OIDCAudience == "" {
		return errors.New("OIDC_ISSUER and OIDC_AUDIENCE are required for api")
	}
	if len(cfg.BFF.Realms) != 2 {
		return errors.New("console and admin BFF configurations are required for api")
	}
	realms := make(map[string]bool, len(cfg.BFF.Realms))
	for _, realm := range cfg.BFF.Realms {
		if err := bff.ValidateConfig(realm); err != nil {
			return fmt.Errorf("invalid %s BFF configuration: %w", realm.Realm, err)
		}
		realms[realm.Realm] = true
	}
	if !realms["console"] || !realms["admin"] {
		return errors.New("console and admin BFF configurations are required for api")
	}
	return nil
}

func newAPIServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

func healthcheck(url string) error {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck status %d", resp.StatusCode)
	}
	return nil
}
