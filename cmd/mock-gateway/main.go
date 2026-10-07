// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nomifun/nomifun-model-gateway/mock"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8788", "HTTP listen address")
	delay := flag.Duration("stream-delay", 0, "Delay between synthetic SSE events (for example 10ms)")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("unexpected positional arguments")
	}
	if *delay < 0 {
		log.Fatal("stream-delay must not be negative")
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal("mock listener could not start")
	}
	server := &http.Server{Handler: mock.New(mock.Config{APIKey: os.Getenv("NMG_MOCK_API_KEY"), StreamDelay: *delay}), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
	// Deliberately no total WriteTimeout: streaming ends on cancellation.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Mock gateway listening at http://%s", listener.Addr().String())
	if err = server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("mock HTTP server stopped unexpectedly")
	}
}
