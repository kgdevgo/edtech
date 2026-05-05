package main

import (
	"context"
	"database/sql"
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
	"edtech-pg/internal/handlers"
	"edtech-pg/internal/storage"
	"edtech-pg/internal/worker"

	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	slog.Info("Starting edtech-api...")

	// Database
	host := getEnv("DB_HOST", "localhost")
	port := getEnv("DB_PORT", "5432")
	user := getEnv("DB_USER", "postgres")
	password := getEnv("DB_PASSWORD", "secret")
	dbname := getEnv("DB_NAME", "edtech")

	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

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

	// Kafka Writer (for Outbox Relay)
	kafkaBroker := getEnv("KAFKA_BROKER", "localhost:29092")
	kafkaWriter := &kafka.Writer{
		Addr:     kafka.TCP(kafkaBroker),
		Topic:    "enrollments",
		Balancer: &kafka.LeastBytes{},
	}
	defer kafkaWriter.Close()

	// Redis
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	redisClient := redis.NewClient(&redis.Options{
		Addr:         redisAddr,
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

	// Background Workers (Outbox Relay)
	workerCtx, workerCancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	relayWorker := worker.NewRelay(db, kafkaWriter)
	wg.Add(1)
	go func() {
		defer wg.Done()
		relayWorker.Start(workerCtx)
	}()

	// App Logic & Routers
	jwtSecret := getEnv("JWT_SECRET", "")
	tokenManager, err := auth.NewTokenManager(jwtSecret)
	if err != nil {
		log.Fatal("Failed to create token manager:", err)
	}

	store := storage.New(db)
	h := handlers.New(store, tokenManager)

	applyMiddlewares := func(h http.HandlerFunc) http.Handler {
		return handlers.RecoveryMiddleware(
			handlers.RequestIDMiddleware(
				handlers.LoggingMiddleware(
					handlers.TimeoutMiddleware(h))))
	}

	applyAuthMiddlewares := func(hf http.HandlerFunc) http.Handler {
		return handlers.RecoveryMiddleware(
			handlers.RequestIDMiddleware(
				handlers.LoggingMiddleware(
					h.AuthMiddleware(handlers.TimeoutMiddleware(hf)),
				),
			),
		)
	}

	applyLoginMiddlewares := func(hf http.HandlerFunc) http.Handler {
		return handlers.RecoveryMiddleware(
			handlers.RequestIDMiddleware(
				handlers.LoggingMiddleware(
					limiter.Middleware(
						handlers.TimeoutMiddleware(hf),
					),
				),
			),
		)
	}

	mux := http.NewServeMux()
	mux.Handle("POST /login", applyLoginMiddlewares(h.Login))
	mux.Handle("POST /courses", applyMiddlewares(h.CreateCourse))
	mux.Handle("GET /courses", applyMiddlewares(h.GetCourses))
	mux.Handle("POST /students", applyMiddlewares(h.CreateStudent))
	mux.Handle("POST /enroll", applyAuthMiddlewares(h.Enroll))
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.Handle("GET /health", http.HandlerFunc(h.Health))
	mux.Handle("GET /ready", http.HandlerFunc(h.Ready))

	srv := &http.Server{
		Addr:    ":8080",
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
