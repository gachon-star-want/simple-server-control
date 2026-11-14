package api

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	loginMaxFails = 5
	loginWindow   = 15 * time.Minute // failures older than this are forgotten
	loginLockout  = 15 * time.Minute
)

type loginFail struct {
	count       int
	first       time.Time
	lockedUntil time.Time
}

// loginLimiter blocks an IP for loginLockout after loginMaxFails wrong passwords within loginWindow.
// shortcut: in-memory, so a restart clears it; fine for one admin panel, move to SQLite if ssc ever runs multiple instances.
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string]*loginFail
	now   func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: make(map[string]*loginFail), now: time.Now}
}

// retryAfter returns how long ip must still wait (0 = allowed).
func (l *loginLimiter) retryAfter(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f := l.fails[ip]; f != nil {
		if d := f.lockedUntil.Sub(l.now()); d > 0 {
			return d
		}
	}
	return 0
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, f := range l.fails { // prune so the map cannot grow without bound
		if now.Sub(f.first) > loginWindow && now.After(f.lockedUntil) {
			delete(l.fails, k)
		}
	}
	f := l.fails[ip]
	if f == nil {
		f = &loginFail{first: now}
		l.fails[ip] = f
	}
	f.count++
	if f.count >= loginMaxFails {
		f.lockedUntil = now.Add(loginLockout)
		f.count = 0
		f.first = now
	}
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}

type viaProxyKey struct{}

// ViaProxy marks requests arriving on the Caddy-only Unix socket, whose X-Forwarded-For is set by Caddy.
func ViaProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), viaProxyKey{}, true)))
	})
}

// clientIP never trusts client-supplied headers on direct TCP connections; behind Caddy it takes the
// last X-Forwarded-For hop, which Caddy writes from the real peer address.
func clientIP(r *http.Request) string {
	if via, _ := r.Context().Value(viaProxyKey{}).(bool); via {
		xff := r.Header.Get("X-Forwarded-For")
		if i := strings.LastIndex(xff, ","); i >= 0 {
			xff = xff[i+1:]
		}
		if ip := strings.TrimSpace(xff); ip != "" {
			return ip
		}
		return "proxy"
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
