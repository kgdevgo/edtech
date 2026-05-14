package main

import (
	"context"
	"database/sql"
	"edtech-pg/internal/events"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"edtech-pg/internal/auth"
	"edtech-pg/internal/config"
	"edtech-pg/internal/handlers"
	"edtech-pg/internal/storage"
	"edtech-pg/internal/worker"

	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

type Middleware func(handler http.Handler) http.Handler

func chain(h http.HandlerFunc, middlewares ...Middleware) http.Handler {
	var final http.Handler = h
	for i := len(middlewares) - 1; i >= 0; i-- {
		final = middlewares[i](final)
	}
	return final
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)
	slog.Info("Starting edtech-api...")

	cfg := config.Load()

	// Database

	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.DB.Host, cfg.DB.Port, cfg.DB.User, cfg.DB.Password, cfg.DB.Name)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Fatal("Failed to connect to database", err)
	}
	slog.Info("Successfully connected to Database!")

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(10 * time.Minute)

	// Kafka Writer (Outbox Relay) & Reader (API Consumer)
	kafkaWriter := &kafka.Writer{
		Addr:                   kafka.TCP(cfg.Kafka.Broker),
		Balancer:               &kafka.LeastBytes{},
		AllowAutoTopicCreation: true,
	}
	defer kafkaWriter.Close()

	kafkaReader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{cfg.Kafka.Broker},
		Topic:   events.TopicPaymentCompleted,
		GroupID: "api-results-group",
	})
	defer kafkaReader.Close()

	// Redis & Rate Limiter
	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		DialTimeout:  1 * time.Second,
		ReadTimeout:  1 * time.Second,
		WriteTimeout: 1 * time.Second,
		PoolSize:     10,
	})
	defer redisClient.Close()

	if err = redisClient.Ping(context.Background()).Err(); err != nil {
		slog.Warn("Failed to connect to Redis, rate limiting will be disabled (Fail-Open", "error", err)
	} else {
		slog.Info("Successfully connected to Redis")
	}

	limiter := handlers.NewRateLimiter(redisClient, 5, 1*time.Minute)

	// App Logic
	tokenManager, err := auth.NewTokenManager(cfg.App.JWTSecret)
	if err != nil {
		log.Fatal("Failed to create token manager:", err)
	}

	store := storage.New(db)
	h := handlers.New(store, tokenManager)

	// Background Workers
	workerCtx, workerCancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	relayWorker := worker.NewRelay(db, kafkaWriter)
	wg.Add(1)
	go func() {
		defer wg.Done()
		relayWorker.Start(workerCtx)
	}()

	apiConsumer := worker.NewConsumer(kafkaReader, store)
	wg.Add(1)
	go func() {
		defer wg.Done()
		apiConsumer.Start(workerCtx)
	}()

	// Middlewares & Routers

	baseStack := []Middleware{
		handlers.RecoveryMiddleware,
		handlers.RequestIDMiddleware,
		handlers.LoggingMiddleware,
	}

	mux := http.NewServeMux()

	// Unauthenticated routes
	mux.Handle("POST /login", chain(h.Login, append(baseStack, limiter.Middleware, handlers.TimeoutMiddleware)...))
	mux.Handle("POST /courses", chain(h.CreateCourse, append(baseStack, handlers.TimeoutMiddleware)...))
	mux.Handle("GET /courses", chain(h.GetCourses, append(baseStack, handlers.TimeoutMiddleware)...))
	mux.Handle("POST /students", chain(h.CreateStudent, append(baseStack, handlers.TimeoutMiddleware)...))

	// Authenticated routes
	mux.Handle("POST /enroll", chain(h.Enroll, append(baseStack, h.AuthMiddleware, handlers.TimeoutMiddleware)...))

	// Metrics & health checks
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.Handle("GET /health", http.HandlerFunc(h.Health))
	mux.Handle("GET /ready", http.HandlerFunc(h.Ready))

	srv := &http.Server{
		Addr:    ":" + cfg.App.Port,
		Handler: mux,
	}

	go func() {
		slog.Info("Server started on http://localhost:8080")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	// Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	<-quit
	slog.Info("Shutting down API server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown:", "error", err)
	}

	slog.Info("Shutting down background workers...")
	workerCancel()
	wg.Wait()

	slog.Info("API Server exiting gracefully")
}
