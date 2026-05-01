package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RateLimiter struct {
	client *redis.Client
	limit  int64
	window time.Duration
}

func NewRateLimiter(client *redis.Client, limit int64, window time.Duration) *RateLimiter {
	return &RateLimiter{
		client: client,
		limit:  limit,
		window: window,
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = strings.Split(r.RemoteAddr, ":")[0]
		}

		key := fmt.Sprintf("ratelimit:%s:%s", r.URL.Path, ip)

		ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
		defer cancel()

		pipe := rl.client.TxPipeline()
		incr := pipe.Incr(ctx, key)
		pipe.Expire(ctx, key, rl.window)

		_, err = pipe.Exec(ctx)
		if err != nil {
			slog.Error("rate limiter redis failure (failing open)", "error", err, "ip", ip)
			next.ServeHTTP(w, r)
			return
		}

		if incr.Val() > rl.limit {
			slog.Warn("rate limit exceeded", "ip", ip, "path", r.URL.Path)
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
