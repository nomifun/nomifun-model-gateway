// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/gofrs/flock"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// One process owns both migration and interrupted-request recovery. The lock
// is held until HTTP workers stop; OS/database disconnection releases it after
// crashes. These locks are an explicit single-node deployment boundary.
func exclusiveInstance(ctx context.Context, driver, dsn string, onLost ...func()) (func(), error) {
	if driver == "sqlite" {
		lock := flock.New(dsn + ".instance.lock")
		ok, err := lock.TryLock()
		if err != nil {
			return nil, errors.New("could not acquire SQLite instance lock")
		}
		if !ok {
			return nil, errors.New("another gateway process owns this SQLite database")
		}
		return func() { _ = lock.Unlock(); _ = lock.Close() }, nil
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, errors.New("could not open PostgreSQL instance lock")
	}
	db.SetMaxOpenConns(1)
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	connection, err := db.Conn(lockCtx)
	if err != nil {
		_ = db.Close()
		return nil, errors.New("could not connect PostgreSQL instance lock")
	}
	var locked bool
	if err = connection.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock(763927841184512347)").Scan(&locked); err != nil || !locked {
		_ = connection.Close()
		_ = db.Close()
		return nil, errors.New("another gateway process owns this PostgreSQL database or its instance lock is unavailable")
	}
	guardCtx, guardCancel := context.WithCancel(ctx)
	guardDone := monitorOwnership(guardCtx, func(probeCtx context.Context) error {
		var held bool
		const lockID = int64(763927841184512347)
		err := connection.QueryRowContext(probeCtx, "SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND pid = pg_backend_pid() AND classid::bigint = $1 AND objid::bigint = $2 AND granted)", lockID>>32, lockID&0xffffffff).Scan(&held)
		if err != nil || !held {
			return errors.New("exclusive PostgreSQL connection or lock was lost")
		}
		return nil
	}, func() {
		for _, callback := range onLost {
			if callback != nil {
				callback()
			}
		}
	})
	return func() {
		guardCancel()
		<-guardDone
		releaseCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_, _ = connection.ExecContext(releaseCtx, "SELECT pg_advisory_unlock(763927841184512347)")
		_ = connection.Close()
		_ = db.Close()
	}, nil
}

// The guardian never reconnects or reacquires ownership silently. The parent
// cancels admission and existing provider contexts before normal worker drain.
func monitorOwnership(ctx context.Context, probe func(context.Context) error, onLost func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check, cancel := context.WithTimeout(ctx, time.Second)
				err := probe(check)
				cancel()
				if err != nil {
					if ctx.Err() == nil && onLost != nil {
						onLost()
					}
					return
				}
			}
		}
	}()
	return done
}
func healthcheck() error {
	host, port, err := net.SplitHostPort(env("NMG_LISTEN", "127.0.0.1:8789"))
	if err != nil {
		return errors.New("invalid listen address")
	}
	loopback := "127.0.0.1"
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		loopback = "::1"
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get("http://" + net.JoinHostPort(loopback, port) + "/readyz")
	if err != nil {
		return errors.New("gateway is not ready")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("gateway is not ready")
	}
	return nil
}
