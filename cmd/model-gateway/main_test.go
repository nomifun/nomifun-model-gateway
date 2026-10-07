// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestExclusiveSQLiteProcessOwnershipAndRecoveryRelease(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "gateway.db")
	release, err := exclusiveInstance(context.Background(), "sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if extra, err := exclusiveInstance(context.Background(), "sqlite", dsn); err == nil {
		extra()
		t.Fatal("second process owner accepted")
	}
	release()
	second, err := exclusiveInstance(context.Background(), "sqlite", dsn)
	if err != nil {
		t.Fatal("released instance lock unavailable", err)
	}
	second()
}
func TestOwnershipGuardianStopsWithoutReacquiring(t *testing.T) {
	lost := make(chan struct{})
	calls := 0
	done := monitorOwnership(context.Background(), func(context.Context) error { calls++; return errors.New("synthetic disconnected lock") }, func() { close(lost) })
	select {
	case <-lost:
	case <-time.After(2 * time.Second):
		t.Fatal("lost lock did not trigger fail-stop")
	}
	<-done
	if calls != 1 {
		t.Fatal("guardian attempted to reacquire ownership")
	}
	t.Setenv("NMG_RESPONSE_AFFINITY_TTL", "")
	ttl, err := affinityDuration()
	if err != nil || ttl != 30*24*time.Hour {
		t.Fatal("default response affinity retention shorter than provider continuation")
	}
	t.Setenv("NMG_RESPONSE_AFFINITY_TTL", "720h")
	if _, err = affinityDuration(); err != nil {
		t.Fatal("long provider retention rejected")
	}
}
func TestHealthcheckUsesLocalReadinessBeforeEnvironmentSecrets(t *testing.T) {
	var status atomic.Int64
	status.Store(http.StatusOK)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" || r.Header.Get("Authorization") != "" {
			t.Error("health probe target or credential boundary")
		}
		w.WriteHeader(int(status.Load()))
	}))
	defer service.Close()
	_, port, err := net.SplitHostPort(service.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("NMG_LISTEN", net.JoinHostPort("0.0.0.0", port))
	t.Setenv("NMG_MASTER_KEY", "")
	if err = healthcheck(); err != nil {
		t.Fatal(err)
	}
	status.Store(http.StatusServiceUnavailable)
	if healthcheck() == nil {
		t.Fatal("unready service passed healthcheck")
	}
}
