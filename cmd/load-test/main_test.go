// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
func TestConfigurationBoundsAndCredentialURLs(t *testing.T) {
	for _, values := range []map[string]string{{"NMG_LOAD_URL": "https://example.test?key=secret"}, {"NMG_LOAD_URL": "https://secret@example.test"}, {"NMG_LOAD_URL": "http://example.test", "NMG_LOAD_API_KEY": "secret"}, {"NMG_LOAD_ENDPOINT": "//other.test"}, {"NMG_LOAD_ENDPOINT": "/healthz?key=secret"}, {"NMG_LOAD_REQUESTS": "100001"}, {"NMG_LOAD_CONCURRENCY": "0"}, {"NMG_LOAD_TOTAL_TIMEOUT": "2h"}, {"NMG_LOAD_API_KEY": "secret\r\nvalue"}} {
		if _, err := configured(env(values)); err == nil {
			t.Fatalf("invalid configuration accepted")
		}
	}
	c, err := configured(env(nil))
	if err != nil || c.URL != "http://127.0.0.1:8789/healthz" || c.Key != "" || c.Method != "GET" {
		t.Fatalf("unsafe defaults %+v %v", c, err)
	}
}
func TestBoundedRunReportsFailuresAndDoesNotFollowRedirects(t *testing.T) {
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++; w.WriteHeader(200) }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("authorization missing")
		}
		w.Header().Set("Location", target.URL)
		w.WriteHeader(302)
	}))
	defer s.Close()
	c, err := configured(env(map[string]string{"NMG_LOAD_URL": s.URL, "NMG_LOAD_API_KEY": "synthetic", "NMG_LOAD_REQUESTS": "7", "NMG_LOAD_CONCURRENCY": "2"}))
	if err != nil {
		t.Fatal(err)
	}
	r := run(context.Background(), c)
	if r.Completed != 7 || r.Errors != 7 || r.StatusCounts[302] != 7 || targetCalls != 0 {
		t.Fatalf("bad bounded results %+v", r)
	}
}
func TestSuccessfulRunAndCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer s.Close()
	c, err := configured(env(map[string]string{"NMG_LOAD_URL": s.URL, "NMG_LOAD_REQUESTS": "9", "NMG_LOAD_CONCURRENCY": "3"}))
	if err != nil {
		t.Fatal(err)
	}
	r := run(context.Background(), c)
	if r.Completed != 9 || r.Successful != 9 || r.Errors != 0 || r.LatencyMS.P95 <= 0 || r.RequestsPerSecond <= 0 {
		t.Fatalf("invalid success result %+v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.TotalTimeout = time.Second
	r = run(ctx, c)
	if r.Completed != 0 {
		t.Fatalf("cancellation dispatched requests %+v", r)
	}
}
