package main

import (
	"context"
	"database/sql"
	"edtech-pg/internal/handlers"
	"edtech-pg/internal/storage"
	"edtech-pg/internal/worker"
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

	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/segmentio/kafka-go"
)

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func main() {
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
	fmt.Println("Successfully connected!")

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	kafkaBroker := getEnv("KAFKA_BROKER", "localhost:29092")
	topicName := "enrollments"

	kafkaWriter := &kafka.Writer{
		Addr:     kafka.TCP(kafkaBroker),
		Topic:    topicName,
		Balancer: &kafka.LeastBytes{},
	}
	defer kafkaWriter.Close()

	workerCtx, workerCancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	wg.Add(1)
	go runEmailWorker(workerCtx, &wg, kafkaBroker, topicName)
	relayWorker := worker.NewRelay(db, kafkaWriter)
	wg.Add(1)
	go func() {
		defer wg.Done()
		relayWorker.Start(workerCtx)
	}()

	store := storage.New(db)
	h := handlers.New(store)

	applyMiddlewares := func(h http.HandlerFunc) http.Handler {
		return handlers.RecoveryMiddleware(
			handlers.RequestIDMiddleware(
				handlers.LoggingMiddleware(
					handlers.TimeoutMiddleware(h))))
	}

	mux := http.NewServeMux()
	mux.Handle("POST /courses", applyMiddlewares(h.CreateCourse))
	mux.Handle("GET /courses", applyMiddlewares(h.GetCourses))
	mux.Handle("POST /students", applyMiddlewares(h.CreateStudent))
	mux.Handle("POST /enroll", applyMiddlewares(h.Enroll))

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

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	<-quit
	slog.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown:", "error", err)
	}

	slog.Info("Shutting down background workers...")
	workerCancel()
	wg.Wait()

	if err := kafkaWriter.Close(); err != nil {
		slog.Error("Failed to close writer", "error", err)
	}

	slog.Info("Server exiting gracefully")

}

func runEmailWorker(ctx context.Context, wg *sync.WaitGroup, broker, topic string) {
	defer wg.Done()

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{broker},
		Topic:   topic,
		GroupID: "email-sender-group",
	})
	defer reader.Close()

	slog.Info("Email worker started, waiting for messages...")

	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				slog.Info("Email worker gracefully stopped")
				return
			}
			slog.Error("worker read error", "error", err)
			break
		}
		slog.Info("[WORKER] Sending Welcome Email...",
			"event", string(msg.Value),
			"partition", msg.Partition,
			"offset", msg.Offset)
	}
}
