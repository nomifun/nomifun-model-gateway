// SPDX-License-Identifier: Apache-2.0
package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedConsoleAndRouteBoundary(t *testing.T) {
	handler := Handler()
	for _, test := range []struct {
		path string
		code int
	}{{"/console/", 200}, {"/console/orders", 200}, {"/console/assets/missing.js", 404}, {"/console/missing.js", 404}, {"/api/console/v1/me", 404}, {"/console/../secret", 404}, {"/console", 308}} {
		t.Run(test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != test.code {
				t.Fatalf("got %d, want %d", recorder.Code, test.code)
			}
			if test.code == 200 && !strings.Contains(recorder.Body.String(), "id=\"root\"") {
				t.Fatal("missing embedded React entry")
			}
		})
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/console/", nil))
	if recorder.Code != 405 {
		t.Fatal("console accepted a mutation")
	}
}
func TestConsoleContentSecurityAndCache(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/console/", nil))
	if recorder.Header().Get("Cache-Control") != "no-store" || !strings.Contains(recorder.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("missing console security headers")
	}
	if recorder.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("payment navigation may leak console URL")
	}
	head := httptest.NewRecorder()
	Handler().ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/console/", nil))
	if head.Body.Len() != 0 {
		t.Fatal("HEAD included body")
	}
}
