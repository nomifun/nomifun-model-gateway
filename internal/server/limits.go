// SPDX-License-Identifier: Apache-2.0
package server

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// This limiter is deliberately process-local. A deployment shares neither its
// counters nor startup recovery with another process; multi-node operation
// requires an external coordinated limiter and exclusive recovery procedures.
type limitBucket struct {
	start, last              time.Time
	requests, tokens, active int64
}
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*limitBucket
	now     func() time.Time
}

func newLimiter() *limiter { return &limiter{buckets: map[string]*limitBucket{}, now: time.Now} }
func (l *limiter) acquire(id string, tokens int64, rpm, tpm, concurrent *int64) (func(), time.Duration, bool) {
	l.mu.Lock()
	now := l.now()
	b := l.buckets[id]
	if b == nil {
		if len(l.buckets) >= 10000 {
			for id, entry := range l.buckets {
				if entry.active == 0 && now.Sub(entry.last) > 2*time.Minute {
					delete(l.buckets, id)
				}
			}
		}
		if len(l.buckets) >= 10000 {
			l.mu.Unlock()
			return nil, time.Minute, false
		}
		b = &limitBucket{start: now}
		l.buckets[id] = b
	}
	if now.Sub(b.start) >= time.Minute {
		b.start = now
		b.requests = 0
		b.tokens = 0
	}
	b.last = now
	retry := time.Minute - now.Sub(b.start)
	if tokens < 0 || b.requests == 9223372036854775807 || b.tokens > 9223372036854775807-tokens || b.active == 9223372036854775807 || (rpm != nil && (*rpm <= b.requests)) || (tpm != nil && (*tpm < tokens || b.tokens > *tpm-tokens)) || (concurrent != nil && *concurrent <= b.active) {
		l.mu.Unlock()
		return nil, retry, false
	}
	b.requests++
	b.tokens += tokens
	b.active++
	l.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { l.mu.Lock(); b.active--; b.last = l.now(); l.mu.Unlock() }) }, 0, true
}
func (l *limiter) authIP(ip string) (time.Duration, bool) {
	rpm := int64(20)
	release, retry, ok := l.acquire("auth:"+ip, 0, &rpm, nil, nil)
	if ok {
		release()
	}
	return retry, ok
}
func retrySeconds(retry time.Duration) string {
	n := int64((retry + time.Second - 1) / time.Second)
	if n < 1 {
		n = 1
	}
	return strconv.FormatInt(n, 10)
}
func strconvID(id int64) string { return strconv.FormatInt(id, 10) }

func (s *Server) prometheus(c *gin.Context) {
	s.metricsMu.Lock()
	keys := make([]metricKey, 0, len(s.metrics))
	counts := map[metricKey]uint64{}
	for key, n := range s.metrics {
		keys = append(keys, key)
		counts[key] = n
	}
	s.metricsMu.Unlock()
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.route != b.route {
			return a.route < b.route
		}
		if a.method != b.method {
			return a.method < b.method
		}
		return a.status < b.status
	})
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	c.Writer.WriteString("# HELP nomifun_gateway_http_requests_total Completed gateway HTTP requests.\n# TYPE nomifun_gateway_http_requests_total counter\n")
	for _, key := range keys {
		fmt.Fprintf(c.Writer, "nomifun_gateway_http_requests_total{route=%q,method=%q,status=%q} %d\n", key.route, key.method, strconv.Itoa(key.status), counts[key])
	}
}
