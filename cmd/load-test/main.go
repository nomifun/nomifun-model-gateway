// SPDX-License-Identifier: Apache-2.0
// load-test performs bounded HTTP checks without printing URLs, keys or bodies.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type config struct {
	URL            string
	Method         string
	Key            string
	Body           []byte
	Requests       int
	Concurrency    int
	RequestTimeout time.Duration
	TotalTimeout   time.Duration
}
type latency struct {
	Min float64 `json:"min"`
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	Max float64 `json:"max"`
}
type report struct {
	Requested         int         `json:"requested"`
	Completed         int         `json:"completed"`
	Successful        int         `json:"successful"`
	Errors            int         `json:"errors"`
	StatusCounts      map[int]int `json:"status_counts"`
	ElapsedSeconds    float64     `json:"elapsed_seconds"`
	RequestsPerSecond float64     `json:"requests_per_second"`
	LatencyMS         latency     `json:"latency_ms"`
}

func configured(get func(string) string) (config, error) {
	c := config{Requests: 100, Concurrency: 4, RequestTimeout: 10 * time.Second, TotalTimeout: 60 * time.Second, Method: http.MethodGet, Key: get("NMG_LOAD_API_KEY")}
	base := get("NMG_LOAD_URL")
	if base == "" {
		base = "http://127.0.0.1:8789"
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("NMG_LOAD_URL must be an HTTP(S) root URL without credentials, query or fragment")
	}
	if strings.ContainsAny(c.Key, "\r\n") {
		return c, errors.New("invalid NMG_LOAD_API_KEY header")
	}
	if c.Key != "" && u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return c, errors.New("authenticated load requests require HTTPS or a loopback URL")
		}
	}
	endpoint := get("NMG_LOAD_ENDPOINT")
	if endpoint == "" {
		endpoint = "/healthz"
	}
	e, err := url.Parse(endpoint)
	if err != nil || !strings.HasPrefix(endpoint, "/") || e.IsAbs() || e.Host != "" || e.RawQuery != "" || e.Fragment != "" || strings.HasPrefix(endpoint, "//") {
		return c, errors.New("NMG_LOAD_ENDPOINT must be an absolute path without a query or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/") + e.Path
	u.RawPath = ""
	c.URL = u.String()
	if method := get("NMG_LOAD_METHOD"); method != "" {
		c.Method = strings.ToUpper(method)
	}
	if c.Method != http.MethodGet && c.Method != http.MethodPost {
		return c, errors.New("NMG_LOAD_METHOD must be GET or POST")
	}
	for _, setting := range []struct {
		name   string
		target *int
		max    int
	}{{"NMG_LOAD_REQUESTS", &c.Requests, 100000}, {"NMG_LOAD_CONCURRENCY", &c.Concurrency, 256}} {
		if value := get(setting.name); value != "" {
			n, e := strconv.Atoi(value)
			if e != nil || n < 1 || n > setting.max {
				return c, fmt.Errorf("%s is outside its bounded range", setting.name)
			}
			*setting.target = n
		}
	}
	for _, setting := range []struct {
		name   string
		target *time.Duration
		max    time.Duration
	}{{"NMG_LOAD_REQUEST_TIMEOUT", &c.RequestTimeout, 5 * time.Minute}, {"NMG_LOAD_TOTAL_TIMEOUT", &c.TotalTimeout, 30 * time.Minute}} {
		if value := get(setting.name); value != "" {
			d, e := time.ParseDuration(value)
			if e != nil || d < time.Millisecond || d > setting.max {
				return c, fmt.Errorf("%s is outside its bounded range", setting.name)
			}
			*setting.target = d
		}
	}
	if file := get("NMG_LOAD_BODY_FILE"); file != "" {
		f, e := os.Open(file)
		if e != nil {
			return c, errors.New("could not open NMG_LOAD_BODY_FILE")
		}
		defer f.Close()
		c.Body, e = io.ReadAll(io.LimitReader(f, (5<<20)+1))
		if e != nil || len(c.Body) > 5<<20 || !json.Valid(c.Body) {
			return c, errors.New("NMG_LOAD_BODY_FILE must contain valid JSON no larger than 5 MiB")
		}
		if c.Method != http.MethodPost {
			return c, errors.New("NMG_LOAD_BODY_FILE requires POST")
		}
	}
	return c, nil
}

func run(parent context.Context, c config) report {
	ctx, cancel := context.WithTimeout(parent, c.TotalTimeout)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = c.Concurrency
	transport.MaxIdleConnsPerHost = c.Concurrency
	transport.DialContext = (&net.Dialer{Timeout: c.RequestTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = c.RequestTimeout
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: c.RequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r := report{Requested: c.Requests, StatusCounts: map[int]int{}}
	var mu sync.Mutex
	var workers sync.WaitGroup
	var durations []float64
	jobs := make(chan struct{})
	started := time.Now()
	for i := 0; i < c.Concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range jobs {
				if ctx.Err() != nil {
					return
				}
				t := time.Now()
				req, _ := http.NewRequestWithContext(ctx, c.Method, c.URL, bytes.NewReader(c.Body))
				if len(c.Body) > 0 {
					req.Header.Set("Content-Type", "application/json")
				}
				if c.Key != "" {
					req.Header.Set("Authorization", "Bearer "+c.Key)
				}
				resp, err := client.Do(req)
				status := 0
				if err == nil {
					status = resp.StatusCode
					n, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, (64<<20)+1))
					resp.Body.Close()
					if readErr != nil || n > 64<<20 {
						err = errors.New("response unavailable or too large")
					}
				}
				mu.Lock()
				r.Completed++
				if status != 0 {
					r.StatusCounts[status]++
				}
				if err == nil && status >= 200 && status < 300 {
					r.Successful++
				} else {
					r.Errors++
				}
				durations = append(durations, float64(time.Since(t))/float64(time.Millisecond))
				mu.Unlock()
			}
		}()
	}
send:
	for i := 0; i < c.Requests; i++ {
		select {
		case jobs <- struct{}{}:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	workers.Wait()
	r.ElapsedSeconds = time.Since(started).Seconds()
	if r.ElapsedSeconds > 0 {
		r.RequestsPerSecond = float64(r.Completed) / r.ElapsedSeconds
	}
	if len(durations) > 0 {
		sort.Float64s(durations)
		quantile := func(p float64) float64 { return durations[int(math.Ceil(float64(len(durations))*p))-1] }
		r.LatencyMS = latency{durations[0], quantile(.5), quantile(.95), durations[len(durations)-1]}
	}
	return r
}

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "load-test accepts configuration through NMG_LOAD_* environment variables only")
		os.Exit(2)
	}
	c, err := configured(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(2)
	}
	r := run(context.Background(), c)
	if json.NewEncoder(os.Stdout).Encode(r) != nil {
		os.Exit(2)
	}
	if r.Errors > 0 || r.Completed != r.Requested {
		os.Exit(1)
	}
}
