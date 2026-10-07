// SPDX-License-Identifier: Apache-2.0
// Package server composes the native relay and independently operated console.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/payment"
	"github.com/nomifun/nomifun-model-gateway/internal/relay"
	"github.com/nomifun/nomifun-model-gateway/internal/store"
	"gorm.io/gorm"
)

type Options struct {
	Logger  *slog.Logger
	Console http.Handler
}

type Server struct {
	Store         *store.Store
	Billing       *billing.Service
	Relay         *relay.Service
	Payment       *payment.Service
	PaymentConfig *payment.ConfigRepository
	engine        *gin.Engine
	logger        *slog.Logger
	limits        *limiter
	metricsMu     sync.Mutex
	metrics       map[metricKey]uint64
}
type metricKey struct {
	route, method string
	status        int
}

// New has no global default logger and never records raw paths, query strings,
// headers or model content. Authentication credentials never enter telemetry.
func New(st *store.Store, bill *billing.Service, native *relay.Service, pay *payment.Service, configs *payment.ConfigRepository, options Options) *Server {
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	s := &Server{Store: st, Billing: bill, Relay: native, Payment: pay, PaymentConfig: configs, logger: logger, limits: newLimiter(), metrics: map[metricKey]uint64{}}
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.RedirectTrailingSlash = false
	e.RedirectFixedPath = false
	e.UseRawPath = true
	e.UnescapePathValues = true
	e.HandleMethodNotAllowed = true
	_ = e.SetTrustedProxies(nil)
	s.engine = e
	e.Use(s.middleware())
	e.GET("/", func(c *gin.Context) { c.Redirect(http.StatusTemporaryRedirect, "/console/") })
	e.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	e.GET("/readyz", func(c *gin.Context) {
		db, err := s.Store.DB().DB()
		if err == nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
			defer cancel()
			err = db.PingContext(ctx)
		}
		if err != nil {
			c.JSON(503, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(200, gin.H{"status": "ready"})
	})
	e.GET("/metrics", s.prometheus)
	e.GET("/nomifun/v1/meta", s.meta)
	e.GET("/nomifun/v1/catalog", s.apiAuth(), s.catalog)
	e.GET("/nomifun/v1/account", s.apiAuth(), s.account)
	e.GET("/v1/models", s.apiAuth(), s.models)
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/messages/count_tokens", "/v1/images/generations", "/v1/images/edits", "/v1/embeddings", "/v1/rerank"} {
		e.POST(path, s.apiAuth(), s.native)
	}
	e.POST("/v1beta/models/:operation", s.apiAuth(), s.native)
	s.consoleRoutes()
	e.POST("/api/payments/v1/:provider/webhook", s.webhook)
	e.NoMethod(func(c *gin.Context) {
		s.fail(c, &relay.Error{Status: 405, Code: "method_not_allowed", Message: "This resource does not accept this method."})
	})
	e.NoRoute(func(c *gin.Context) {
		if options.Console != nil && (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && (c.Request.URL.Path == "/console" || strings.HasPrefix(c.Request.URL.Path, "/console/")) {
			// Gin enters NoRoute with an uncommitted 404. The embedded SPA writes
			// its index body without an explicit status, so set the success default
			// before delegation; missing assets may still override it with 404.
			c.Status(http.StatusOK)
			options.Console.ServeHTTP(c.Writer, c.Request)
			return
		}
		s.fail(c, &relay.Error{Status: 404, Code: "not_found", Message: "Resource not found."})
	})
	return s
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.engine.ServeHTTP(w, r) }

func requestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("request identity generation failed")
	}
	return hex.EncodeToString(value[:])
}
func (s *Server) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Header("x-request-id", requestID())
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		defer func() {
			if recovered := recover(); recovered != nil {
				if !c.Writer.Written() {
					s.fail(c, &relay.Error{Status: 500, Code: "internal_error", Message: "The gateway could not complete this request."})
				}
				s.logger.Error("request_panic", "request_id", c.Writer.Header().Get("x-request-id"))
			}
			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			method := c.Request.Method
			switch method {
			case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
			default:
				method = "OTHER"
			}
			key := metricKey{route, method, c.Writer.Status()}
			s.metricsMu.Lock()
			s.metrics[key]++
			s.metricsMu.Unlock()
			s.logger.Info("request", "route", route, "method", method, "status", c.Writer.Status(), "request_id", c.Writer.Header().Get("x-request-id"), "duration_ms", time.Since(started).Milliseconds())
		}()
		// Query credentials are only accepted on the two Gemini native endpoints.
		query, queryErr := url.ParseQuery(c.Request.URL.RawQuery)
		if queryErr != nil {
			s.fail(c, &relay.Error{Status: 400, Code: "invalid_request_error", Message: "Invalid query parameters."})
			return
		}
		if _, present := query["key"]; present && !geminiCredentialRoute(c.Request.URL.Path) {
			s.fail(c, &relay.Error{Status: 401, Code: "invalid_api_key", Message: "Query credentials are not accepted on this resource."})
			return
		}
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" && c.Request.Method != "OPTIONS" && !strings.HasPrefix(c.Request.URL.Path, "/api/payments/") {
			if origin := c.GetHeader("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Host != c.Request.Host {
					s.fail(c, &relay.Error{Status: 403, Code: "forbidden_origin", Message: "Cross-origin console requests are not accepted."})
					return
				}
			}
			if c.GetHeader("Sec-Fetch-Site") == "cross-site" {
				s.fail(c, &relay.Error{Status: 403, Code: "forbidden_origin", Message: "Cross-origin console requests are not accepted."})
				return
			}
		}
		c.Next()
	}
}
func geminiCredentialRoute(path string) bool {
	endpoint, _, ok := relay.Route(path)
	return ok && endpoint == v1.Gemini
}

func credential(r *http.Request, gemini bool) (string, error) {
	var values []string
	for _, name := range []string{"Authorization", "x-api-key", "x-goog-api-key"} {
		header := r.Header.Values(name)
		if len(header) > 1 {
			return "", store.ErrUnauthorized
		}
		if len(header) == 0 {
			continue
		}
		value := header[0]
		if name == "Authorization" {
			parts := strings.Split(value, " ")
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				return "", store.ErrUnauthorized
			}
			value = parts[1]
		}
		if !validCredential(value) {
			return "", store.ErrUnauthorized
		}
		values = append(values, value)
	}
	if query, present := r.URL.Query()["key"]; present {
		if !gemini || len(query) != 1 || !validCredential(query[0]) {
			return "", store.ErrUnauthorized
		}
		values = append(values, query[0])
	}
	if len(values) == 0 {
		return "", store.ErrUnauthorized
	}
	for _, value := range values[1:] {
		if value != values[0] {
			return "", store.ErrUnauthorized
		}
	}
	return values[0], nil
}
func validCredential(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range []byte(value) {
		if ch < 33 || ch > 126 || ch == ',' {
			return false
		}
	}
	return true
}
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
func (s *Server) apiAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		plain, err := credential(c.Request, geminiCredentialRoute(c.Request.URL.Path))
		if err != nil {
			s.fail(c, err)
			return
		}
		u, k, err := s.Store.AuthenticateKey(c.Request.Context(), plain, remoteIP(c.Request), "")
		if err != nil {
			s.fail(c, err)
			return
		}
		c.Set("user", u)
		c.Set("key", k)
		c.Next()
	}
}
func user(c *gin.Context) *core.User  { return c.MustGet("user").(*core.User) }
func key(c *gin.Context) *core.APIKey { return c.MustGet("key").(*core.APIKey) }
func (s *Server) sessionAuth(admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		values := c.Request.Header.Values("Authorization")
		if len(values) != 1 {
			s.fail(c, store.ErrUnauthorized)
			return
		}
		parts := strings.Split(values[0], " ")
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !validCredential(parts[1]) {
			s.fail(c, store.ErrUnauthorized)
			return
		}
		u, err := s.Store.SessionUser(c.Request.Context(), parts[1])
		if err != nil {
			s.fail(c, err)
			return
		}
		if admin && !u.Admin {
			s.fail(c, &relay.Error{Status: 403, Code: "forbidden", Message: "Administrator access is required."})
			return
		}
		c.Set("user", u)
		c.Set("session", parts[1])
		c.Next()
	}
}

func (s *Server) fail(c *gin.Context, err error) {
	var native *relay.Error
	var bill *billing.Error
	switch {
	case errors.As(err, &native):
	case errors.As(err, &bill):
		native = &relay.Error{Status: bill.Status, Code: bill.Code, Message: bill.Message}
	case errors.Is(err, store.ErrKeyExpired):
		native = &relay.Error{Status: 401, Code: "key_expired", Message: "This API key has expired."}
	case errors.Is(err, store.ErrUnauthorized):
		native = &relay.Error{Status: 401, Code: "invalid_api_key", Message: "Credentials are unavailable or invalid."}
	case errors.Is(err, store.ErrModelNotAllowed):
		native = &relay.Error{Status: 403, Code: "model_not_in_plan", Message: "This model is not allowed by this key."}
	case errors.Is(err, store.ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound):
		native = &relay.Error{Status: 404, Code: "not_found", Message: "Resource not found."}
	case errors.Is(err, store.ErrConflict) || errors.Is(err, gorm.ErrDuplicatedKey):
		native = &relay.Error{Status: 409, Code: "conflict", Message: "Resource already exists."}
	case errors.Is(err, payment.ErrOrderNotFound):
		native = &relay.Error{Status: 404, Code: "not_found", Message: "Payment order not found."}
	case errors.Is(err, payment.ErrProviderUnavailable):
		native = &relay.Error{Status: 503, Code: "payment_unavailable", Message: "The selected merchant payment provider is unavailable."}
	case errors.Is(err, payment.ErrUpstream):
		native = &relay.Error{Status: 503, Code: "payment_unavailable", Message: "The merchant could not create or reconcile this order."}
	default:
		native = &relay.Error{Status: 400, Code: "invalid_request_error", Message: "The request could not be accepted. Check the submitted fields."}
	}
	if native.Status == 429 {
		c.Header("Retry-After", "60")
	}
	settings, _ := s.instance(c.Request.Context())
	purchase := ""
	if settings.Operator.PurchaseURL != nil {
		purchase = *settings.Operator.PurchaseURL
	}
	relay.WriteError(c.Writer, c.Request, native, purchase)
	c.Abort()
}
func (s *Server) databaseError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	s.fail(c, &relay.Error{Status: 503, Code: "storage_unavailable", Message: "Gateway storage is unavailable."})
	return true
}
func (s *Server) decode(c *gin.Context, destination any) bool {
	if typ := c.GetHeader("Content-Type"); !strings.HasPrefix(typ, "application/json") {
		s.fail(c, &relay.Error{Status: 415, Code: "unsupported_media_type", Message: "Use application/json."})
		return false
	}
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetReadDeadline(time.Now().Add(60 * time.Second))
	defer controller.SetReadDeadline(time.Time{})
	reader := http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		s.fail(c, &relay.Error{Status: 400, Code: "invalid_request_error", Message: "Provide one valid JSON object with exact integer values."})
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		s.fail(c, &relay.Error{Status: 400, Code: "invalid_request_error", Message: "Provide one JSON object."})
		return false
	}
	return true
}
func (s *Server) id(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		s.fail(c, &relay.Error{Status: 400, Code: "invalid_request_error", Message: "Invalid resource identifier."})
		return 0, false
	}
	return id, true
}
func (s *Server) audit(c *gin.Context, action, resource string) bool {
	if err := s.Store.Audit(c.Request.Context(), user(c).ID, action, resource); err != nil {
		s.databaseError(c, err)
		return false
	}
	return true
}
