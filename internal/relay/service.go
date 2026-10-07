// SPDX-License-Identifier: Apache-2.0
// Package relay forwards same-protocol native HTTP and meters usage beside it.
package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"time"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/billing"
	"github.com/nomifun/nomifun-model-gateway/internal/channel"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"gorm.io/gorm"
)

type Store interface {
	DB() *gorm.DB
	ChannelKey(core.Channel) (string, error)
}
type Config struct {
	RequestBodyLimit, ResponseBodyLimit, EventLimit                         int64
	ConnectTimeout, ResponseHeaderTimeout, IdleTimeout, ResponseAffinityTTL time.Duration
	MaxAttempts                                                             int
	DefaultAnthropicVersion, PurchaseURL                                    string
}
type Service struct {
	store    Store
	billing  *billing.Service
	Channels *channel.Service
	Config   Config
	client   *http.Client
}
type Request struct {
	Key                 core.APIKey
	Model               v1.Model
	RequestID           string
	Native              *NativeRequest
	PurchaseURL         string
	RequireSubscription bool
}

func New(store Store, bill *billing.Service, cfg Config) *Service {
	if cfg.RequestBodyLimit <= 0 {
		cfg.RequestBodyLimit = 64 << 20
	}
	if cfg.ResponseBodyLimit <= 0 {
		cfg.ResponseBodyLimit = 32 << 20
	}
	if cfg.EventLimit <= 0 {
		cfg.EventLimit = 8 << 20
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.ResponseHeaderTimeout <= 0 {
		cfg.ResponseHeaderTimeout = 60 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 90 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.DefaultAnthropicVersion == "" {
		cfg.DefaultAnthropicVersion = "2023-06-01"
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, TLSHandshakeTimeout: cfg.ConnectTimeout, ResponseHeaderTimeout: cfg.ResponseHeaderTimeout, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 100, MaxIdleConnsPerHost: 20, DisableCompression: true}
	return &Service{store: store, billing: bill, Channels: channel.New(store.DB(), cfg.ResponseAffinityTTL), Config: cfg, client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (s *Service) Close() { s.client.CloseIdleConnections() }
func (s *Service) Serve(w http.ResponseWriter, r *http.Request, req Request) {
	purchase := req.PurchaseURL
	if purchase == "" {
		purchase = s.Config.PurchaseURL
	}
	fail := func(err error) {
		var own *Error
		var bill *billing.Error
		if errors.As(err, &own) {
			WriteError(w, r, own, purchase)
		} else if errors.As(err, &bill) {
			WriteError(w, r, &Error{bill.Status, bill.Code, bill.Message}, purchase)
		} else {
			WriteError(w, r, &Error{503, "upstream_unavailable", "The gateway could not complete this request."}, purchase)
		}
	}
	var n NativeRequest
	var err error
	if req.Native != nil {
		n = *req.Native
	} else {
		n, err = InspectRequest(r, s.Config.RequestBodyLimit)
		if err != nil {
			fail(err)
			return
		}
	}
	if n.Model != req.Model.ID {
		fail(&Error{404, "model_not_found", "Model is not in the visible catalog."})
		return
	}
	task, ok := req.Model.TaskEndpoints[n.Task]
	supported := false
	for _, e := range task.Endpoints {
		if e == n.Endpoint {
			supported = true
		}
	}
	if !ok || !supported {
		fail(&Error{400, "unsupported_endpoint", "Model does not support this native endpoint."})
		return
	}
	if n.Task == "chat" && r.URL.Path != "/v1/messages/count_tokens" && n.MaxOutput < 1 {
		fail(&Error{400, "invalid_request_error", "Provide an explicit positive native output-token limit for metered generation."})
		return
	}
	if n.Count < 1 || n.Count > 16 {
		fail(&Error{400, "invalid_request_error", "n must be an integer from 1 through 16."})
		return
	}
	if req.Model.MaxOutputTokens != nil && n.MaxOutput > *req.Model.MaxOutputTokens {
		fail(&Error{400, "invalid_request_error", "Output limit exceeds this model's configured maximum."})
		return
	}
	candidates, bound, err := s.Channels.Candidates(r.Context(), req.Key.ID, n.Model, string(n.Endpoint), n.PreviousResponseID, n.SessionID)
	if err != nil {
		if errors.Is(err, channel.ErrAffinity) {
			code := "invalid_previous_response_id"
			if n.SessionID != "" {
				code = "invalid_session_id"
			}
			fail(&Error{400, code, "The requested upstream binding is unavailable."})
		} else {
			fail(err)
		}
		return
	}
	estimate, err := EstimateUsage(n, req.Model)
	if err != nil {
		fail(err)
		return
	}
	reserved := false
	accepted := false
	unknown := false
	if s.billing != nil {
		hold, e := s.billing.Reserve(r.Context(), billing.Request{RequestID: req.RequestID, UserID: req.Key.UserID, APIKeyID: req.Key.ID, Model: req.Model, Task: n.Task, Endpoint: string(n.Endpoint), EstimatedUsage: estimate, RequireSubscription: req.RequireSubscription})
		if e != nil {
			fail(e)
			return
		}
		if hold.State != "reserved" || hold.UpstreamAccepted {
			fail(&Error{409, "idempotency_conflict", "This request ID has already been dispatched."})
			return
		}
		reserved = true
	}
	finish := func(usage core.Usage, reason string) {
		if !reserved {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if usage.Complete && !unknown {
			if _, e := s.billing.Settle(ctx, req.RequestID, usage); e != nil {
				s.billing.MarkReconciliation(ctx, req.RequestID, "Confirmed usage requires operator reconciliation")
			}
		} else if accepted || unknown {
			s.billing.MarkReconciliationWithUsage(ctx, req.RequestID, reason, usage)
		} else {
			s.billing.Release(ctx, req.RequestID, reason)
		}
	}
	defer func() {
		if reserved {
			finish(core.Usage{}, "Request ended without confirmed terminal usage")
		}
	}()
	attempts := s.Config.MaxAttempts
	if bound {
		attempts = 1
	}
	if attempts > len(candidates) {
		attempts = len(candidates)
	}
	var last *Error
	for i, c := range candidates[:attempts] {
		if r.Context().Err() != nil {
			last = &Error{499, "client_cancelled", "Client cancelled the request."}
			break
		}
		upstream, _ := channel.Supports(c, n.Model, string(n.Endpoint))
		key, e := s.store.ChannelKey(c)
		if e != nil {
			last = &Error{503, "upstream_unavailable", "The selected channel credential is unavailable."}
			continue
		}
		body, contentType, e := rewriteBody(n, upstream)
		if e != nil {
			last = &Error{400, "invalid_request_error", "Invalid native request options."}
			break
		}
		address, e := upstreamURL(c.BaseURL, n, r.URL.Path, upstream, c.Kind, c.APIVersion)
		if e != nil {
			last = &Error{503, "upstream_unavailable", "The selected channel address is invalid."}
			continue
		}
		ctx, cancel := context.WithCancel(r.Context())
		var wrote atomic.Bool
		trace := &httptrace.ClientTrace{WroteHeaders: func() { wrote.Store(true) }, WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				wrote.Store(true)
			}
		}}
		ctx = httptrace.WithClientTrace(ctx, trace)
		upstreamReq, e := http.NewRequestWithContext(ctx, http.MethodPost, address.String(), bytes.NewReader(body))
		if e != nil {
			cancel()
			last = &Error{503, "upstream_unavailable", "The native upstream request could not be created."}
			continue
		}
		upstreamReq.Header.Set("Content-Type", contentType)
		upstreamReq.Header.Set("Accept-Encoding", "identity")
		upstreamReq.Header.Set("User-Agent", "NomiFun-Model-Gateway/1.0")
		if n.Stream {
			upstreamReq.Header.Set("Accept", "text/event-stream")
		} else {
			upstreamReq.Header.Set("Accept", "application/json")
		}
		switch c.Kind {
		case "anthropic":
			upstreamReq.Header.Set("x-api-key", key)
			version := r.Header.Get("anthropic-version")
			if version == "" {
				version = c.APIVersion
			}
			if version == "" {
				version = s.Config.DefaultAnthropicVersion
			}
			upstreamReq.Header.Set("anthropic-version", version)
			if beta := r.Header.Get("anthropic-beta"); beta != "" {
				upstreamReq.Header.Set("anthropic-beta", beta)
			}
		case "gemini":
			upstreamReq.Header.Set("x-goog-api-key", key)
		case "azure":
			upstreamReq.Header.Set("api-key", key)
		default:
			upstreamReq.Header.Set("Authorization", "Bearer "+key)
		}
		if reserved && !accepted {
			if e := s.billing.MarkAccepted(r.Context(), req.RequestID); e != nil {
				cancel()
				last = &Error{503, "billing_unavailable", "Unable to durably dispatch request."}
				break
			}
			accepted = true
		}
		response, e := s.client.Do(upstreamReq)
		if e != nil {
			cancel()
			if wrote.Load() {
				unknown = true
			}
			s.healthFailure(c.ID)
			last = &Error{502, "upstream_unavailable", "Native upstream connection failed."}
			if bound {
				break
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			raw, readErr := readIdle(response.Body, cancel, s.Config.IdleTimeout, s.Config.ResponseBodyLimit)
			response.Body.Close()
			cancel()
			retry := response.StatusCode == 429 || response.StatusCode >= 500
			if retry {
				s.healthFailure(c.ID)
			}
			if readErr != nil || int64(len(raw)) > s.Config.ResponseBodyLimit {
				last = &Error{502, "upstream_unavailable", "Invalid upstream error response."}
				if bound {
					break
				}
				continue
			}
			if retry && !bound && i+1 < attempts {
				continue
			}
			// Never relay redirects: Location could transfer an authenticated client to
			// an untrusted destination. HTTP native rejections are safe to release.
			if response.StatusCode >= 300 && response.StatusCode < 400 {
				last = &Error{502, "upstream_redirect", "The upstream returned a redirect."}
				break
			}
			if reserved {
				if !unknown {
					s.reject(req.RequestID, "Native upstream rejected the request")
				} else {
					finish(core.Usage{}, "An earlier upstream attempt has unknown usage")
				}
				reserved = false
			}
			copyHeaders(w.Header(), response.Header, key)
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(response.StatusCode)
			w.Write(redact(raw, key))
			return
		}
		parser := newUsage(string(n.Endpoint), r.URL.Path)
		var transferErr error
		streamStarted := false
		if n.Stream {
			if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
				response.Body.Close()
				cancel()
				unknown = true
				last = &Error{502, "upstream_unavailable", "The native upstream did not return an SSE stream."}
				s.healthFailure(c.ID)
				if bound {
					break
				}
				continue
			}
			streamStarted, transferErr = s.stream(w, response, ctx, cancel, parser, req.Key.ID, c.ID, n.SessionID, key)
			if transferErr != nil && !streamStarted {
				response.Body.Close()
				cancel()
				unknown = true
				last = &Error{502, "upstream_unavailable", "The native stream ended before its first event."}
				s.healthFailure(c.ID)
				if bound {
					break
				}
				continue
			}
		} else {
			raw, e := readIdle(response.Body, cancel, s.Config.IdleTimeout, s.Config.ResponseBodyLimit)
			if e != nil {
				response.Body.Close()
				cancel()
				unknown = true
				last = &Error{502, "upstream_unavailable", "The upstream response ended before completion."}
				s.healthFailure(c.ID)
				if bound {
					break
				}
				continue
			}
			parser.json(raw)
			if n.Endpoint == "openai-response" {
				var value struct {
					ID string `json:"id"`
				}
				json.Unmarshal(raw, &value)
				if value.ID == "" || s.Channels.BindResponse(ctx, req.Key.ID, value.ID, c.ID) != nil {
					response.Body.Close()
					cancel()
					unknown = true
					last = &Error{503, "affinity_unavailable", "Response affinity could not be committed."}
					break
				}
			}
			copyHeaders(w.Header(), response.Header, key)
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(response.StatusCode)
			_, transferErr = w.Write(raw)
		}
		response.Body.Close()
		cancel()
		usage := parser.finish(transferErr == nil)
		if transferErr == nil && !parser.failed {
			s.healthSuccess(c.ID, req.Key.ID, n.SessionID)
		} else {
			s.healthFailure(c.ID)
		}
		finish(usage, "Accepted upstream response has incomplete or missing terminal usage")
		reserved = false
		return
	}
	if reserved {
		if !unknown {
			s.reject(req.RequestID, "All selected upstreams rejected or were unreachable before acceptance")
		} else {
			finish(core.Usage{}, "An upstream attempt may have been accepted without confirmed usage")
		}
		reserved = false
	}
	if last == nil {
		last = &Error{503, "upstream_unavailable", "No native upstream is available."}
	}
	fail(last)
}

func (s *Service) reject(id, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.billing.ConfirmRejected(ctx, id, reason)
}
func (s *Service) healthFailure(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Channels.Failure(ctx, id)
}
func (s *Service) healthSuccess(id, key int64, session string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Channels.Success(ctx, id, key, session)
}
func redact(raw []byte, secret string) []byte {
	if secret != "" {
		return bytes.ReplaceAll(raw, []byte(secret), []byte("[redacted]"))
	}
	return raw
}
func copyHeaders(dst, src http.Header, secret string) {
	for _, name := range []string{"Content-Type", "Retry-After", "X-RateLimit-Limit-Requests", "X-RateLimit-Remaining-Requests", "X-RateLimit-Limit-Tokens", "X-RateLimit-Remaining-Tokens", "X-RateLimit-Reset-Requests", "X-RateLimit-Reset-Tokens"} {
		if value := src.Get(name); value != "" && (secret == "" || !strings.Contains(value, secret)) {
			dst.Set(name, value)
		}
	}
}
func readIdle(reader io.Reader, cancel context.CancelFunc, idle time.Duration, limit int64) ([]byte, error) {
	timer := time.AfterFunc(idle, cancel)
	defer timer.Stop()
	var out bytes.Buffer
	buf := make([]byte, 32<<10)
	for {
		n, e := reader.Read(buf)
		if n > 0 {
			timer.Reset(idle)
			if int64(out.Len()+n) > limit {
				return nil, errors.New("upstream response limit")
			}
			out.Write(buf[:n])
		}
		if e == io.EOF {
			return out.Bytes(), nil
		}
		if e != nil {
			return nil, e
		}
	}
}

// Stream frames remain raw, including unknown events and CRLF. Only one frame
// is buffered, bounded by EventLimit, so response IDs can be committed before
// response.created is flushed. Idle cancellation also interrupts blocked reads.
func (s *Service) stream(w http.ResponseWriter, response *http.Response, ctx context.Context, cancel context.CancelFunc, p *usageParser, key, account int64, session, secret string) (bool, error) {
	timer := time.AfterFunc(s.Config.IdleTimeout, cancel)
	defer timer.Stop()
	buffer := make([]byte, 0, 32<<10)
	chunk := make([]byte, 32<<10)
	started := false
	emit := func(frame []byte) error {
		id, e := p.event(frame)
		if e != nil {
			return e
		}
		if id != "" && p.endpoint == "openai-response" {
			if e = s.Channels.BindResponse(ctx, key, id, account); e != nil {
				return e
			}
		}
		if !started {
			copyHeaders(w.Header(), response.Header, secret)
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(response.StatusCode)
			started = true
		}
		// Only native error events are redacted. Successful reasoning, tools and
		// unknown events retain their exact bytes.
		if bytes.Contains(frame, []byte(`"type":"error"`)) || bytes.Contains(frame, []byte("event: error")) {
			frame = redact(frame, secret)
		}
		if _, e = w.Write(frame); e != nil {
			return e
		}
		return http.NewResponseController(w).Flush()
	}
	for {
		n, e := response.Body.Read(chunk)
		if n > 0 {
			timer.Reset(s.Config.IdleTimeout)
			buffer = append(buffer, chunk[:n]...)
			for {
				end := eventEnd(buffer)
				if end < 0 {
					break
				}
				if int64(end) > s.Config.EventLimit {
					return started, errors.New("native SSE event limit")
				}
				frame := buffer[:end]
				if e := emit(frame); e != nil {
					return started, e
				}
				buffer = buffer[end:]
			}
			if int64(len(buffer)) > s.Config.EventLimit {
				return started, errors.New("native SSE event limit")
			}
		}
		if e == io.EOF {
			if len(buffer) > 0 {
				if e := emit(buffer); e != nil {
					return started, e
				}
			}
			if !started {
				return started, errors.New("empty native stream")
			}
			return started, nil
		}
		if e != nil {
			return started, e
		}
		if ctx.Err() != nil {
			return started, ctx.Err()
		}
	}
}
func eventEnd(buffer []byte) int {
	a := bytes.Index(buffer, []byte("\n\n"))
	b := bytes.Index(buffer, []byte("\r\n\r\n"))
	if a >= 0 && (b < 0 || a < b) {
		return a + 2
	}
	if b >= 0 {
		return b + 4
	}
	return -1
}

// ProbeChannel verifies the native account's model endpoint without model
// content or billing. It uses the same no-redirect authentication transport.
func (s *Service) ProbeChannel(ctx context.Context, id int64) error {
	var c core.Channel
	if err := s.store.DB().WithContext(ctx).First(&c, id).Error; err != nil {
		return err
	}
	key, e := s.store.ChannelKey(c)
	if e != nil {
		return e
	}
	n := NativeRequest{Endpoint: "openai"}
	path := "/v1/models"
	if c.Kind == "gemini" {
		n.Endpoint = "gemini"
	}
	u, e := upstreamURL(c.BaseURL, n, path, "", c.Kind, c.APIVersion)
	if e != nil {
		return e
	}
	if c.Kind == "gemini" {
		// Gemini native GET models has a different path from content requests.
		parsed, e := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(strings.TrimSuffix(c.BaseURL, "/"), "/v1beta")+"/v1beta/models", nil)
		if e != nil {
			return e
		}
		u = parsed.URL
	}
	if c.Kind == "azure" && strings.Contains(u.Path, "/deployments//models") {
		u.Path = strings.TrimSuffix(u.Path, "/deployments//models") + "/models"
	}
	req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return e
	}
	switch c.Kind {
	case "anthropic":
		req.Header.Set("x-api-key", key)
		version := c.APIVersion
		if version == "" {
			version = s.Config.DefaultAnthropicVersion
		}
		req.Header.Set("anthropic-version", version)
	case "gemini":
		req.Header.Set("x-goog-api-key", key)
	case "azure":
		req.Header.Set("api-key", key)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
	response, e := s.client.Do(req)
	if e != nil {
		s.healthFailure(id)
		return &Error{502, "probe_failed", "Upstream connection failed."}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		s.healthFailure(id)
		return &Error{502, "probe_failed", "Upstream rejected the native probe."}
	}
	s.healthSuccess(id, 0, "")
	return nil
}
