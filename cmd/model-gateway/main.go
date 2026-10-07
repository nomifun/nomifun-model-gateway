// SPDX-License-Identifier: Apache-2.0
// model-gateway runs one independently operated production gateway instance.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	"github.com/nomifun/nomifun-model-gateway/internal/console"
	"github.com/nomifun/nomifun-model-gateway/internal/payment"
	"github.com/nomifun/nomifun-model-gateway/internal/relay"
	"github.com/nomifun/nomifun-model-gateway/internal/server"
	"github.com/nomifun/nomifun-model-gateway/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) > 1 {
		if len(os.Args) == 2 && os.Args[1] == "--healthcheck" {
			if healthcheck() != nil {
				os.Exit(1)
			}
			return
		}
		logger.Error("unsupported_argument")
		os.Exit(1)
	}
	if err := run(logger); err != nil {
		logger.Error("gateway_stopped", "reason", err.Error())
		os.Exit(1)
	}
}
func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func duration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 || parsed > 24*time.Hour {
		return 0, fmt.Errorf("%s must be a positive duration no greater than 24h", name)
	}
	return parsed, nil
}
func affinityDuration() (time.Duration, error) {
	value := os.Getenv("NMG_RESPONSE_AFFINITY_TTL")
	if value == "" {
		return 30 * 24 * time.Hour, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 || parsed > 365*24*time.Hour {
		return 0, errors.New("NMG_RESPONSE_AFFINITY_TTL must be a positive duration no greater than 8760h")
	}
	return parsed, nil
}
func positiveInt(name string, fallback int64) (int64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return n, nil
}
func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	driver := env("NMG_DB_DRIVER", "sqlite")
	dsn := env("NMG_DB_DSN", "data/gateway.db")
	if driver == "sqlite" {
		if strings.ContainsAny(dsn, "?#") || strings.HasPrefix(dsn, "file:") || dsn == ":memory:" {
			return errors.New("production SQLite DSN must be a local file path without URI options")
		}
		if err := os.MkdirAll(filepath.Dir(dsn), 0700); err != nil {
			return errors.New("could not create gateway data directory")
		}
	} else if driver != "postgres" && driver != "postgresql" {
		return errors.New("NMG_DB_DRIVER must be sqlite or postgres")
	}
	var ownershipLost atomic.Bool
	unlock, err := exclusiveInstance(ctx, driver, dsn, func() { ownershipLost.Store(true); logger.Error("instance_ownership_lost"); stop() })
	if err != nil {
		return err
	}
	defer unlock()
	// A lost PostgreSQL lock is detected and stops the former owner. Wait beyond
	// its bounded worker shutdown before examining any interrupted reservations.
	// File locks release only when the prior process stops and need no guard.
	if driver != "sqlite" {
		logger.Info("postgres_recovery_guard", "seconds", 25)
		timer := time.NewTimer(25 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.New("instance ownership lost during startup")
		case <-timer.C:
		}
	}
	st, err := store.Open(store.Config{Driver: driver, DSN: dsn, MasterKey: os.Getenv("NMG_MASTER_KEY")})
	if err != nil {
		return err
	}
	defer st.Close()
	if err = st.BootstrapAdmin(ctx, os.Getenv("NMG_ADMIN_EMAIL"), os.Getenv("NMG_ADMIN_PASSWORD")); err != nil {
		return errors.New("administrator bootstrap failed: check bootstrap environment")
	}
	bill := billing.New(st.DB())
	if err = bill.RecoverPending(ctx); err != nil {
		return errors.New("interrupted request recovery failed")
	}
	connect, err := duration("NMG_CONNECT_TIMEOUT", 10*time.Second)
	if err != nil {
		return err
	}
	header, err := duration("NMG_UPSTREAM_HEADER_TIMEOUT", 60*time.Second)
	if err != nil {
		return err
	}
	idle, err := duration("NMG_STREAM_IDLE_TIMEOUT", 90*time.Second)
	if err != nil {
		return err
	}
	affinity, err := affinityDuration()
	if err != nil {
		return err
	}
	bodyLimit, err := positiveInt("NMG_REQUEST_BODY_LIMIT", 64<<20)
	if err != nil {
		return err
	}
	native := relay.New(st, bill, relay.Config{RequestBodyLimit: bodyLimit, ConnectTimeout: connect, ResponseHeaderTimeout: header, IdleTimeout: idle, ResponseAffinityTTL: affinity, DefaultAnthropicVersion: env("NMG_ANTHROPIC_VERSION", "2023-06-01")})
	defer native.Close()
	configs := payment.NewConfigRepository(st)
	payments := payment.New(st.DB(), bill, configs)
	app := server.New(st, bill, native, payments, configs, server.Options{Logger: logger, Console: console.Handler()})
	listener, err := net.Listen("tcp", env("NMG_LISTEN", "127.0.0.1:8789"))
	if err != nil {
		return errors.New("gateway listen address is unavailable")
	}
	httpServer := &http.Server{Handler: app, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return ctx }}
	finished := make(chan error, 1)
	go func() { finished <- httpServer.Serve(listener) }()
	logger.Info("gateway_started", "address", listener.Addr().String(), "contract_version", "1.0", "deployment", "single_node")
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err = httpServer.Shutdown(shutdownCtx); err != nil {
			_ = httpServer.Close()
		}
		<-finished
	case err = <-finished:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.New("gateway HTTP server failed")
		}
	}
	logger.Info("gateway_stopped", "reason", "shutdown")
	if ownershipLost.Load() {
		return errors.New("exclusive instance ownership was lost")
	}
	return nil
}
