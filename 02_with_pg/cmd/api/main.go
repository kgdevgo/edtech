package main

import (
	"context"
	"database/sql"
	"edtech-pg/internal/handlers"
	"edtech-pg/internal/storage"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

	go runEmailWorker(kafkaBroker, topicName)

	store := storage.New(db)
	h := handlers.New(store, kafkaWriter)

	mux := http.NewServeMux()

	mux.Handle("POST /courses", handlers.TimeoutMiddleware(http.HandlerFunc(h.CreateCourse)))
	mux.Handle("GET /courses", handlers.TimeoutMiddleware(http.HandlerFunc(h.GetCourses)))
	mux.Handle("POST /students", handlers.TimeoutMiddleware(http.HandlerFunc(h.CreateStudent)))
	mux.Handle("POST /enroll", handlers.TimeoutMiddleware(http.HandlerFunc(h.Enroll)))
	mux.Handle("GET /metrics", promhttp.Handler())

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

	if err := kafkaWriter.Close(); err != nil {
		slog.Error("Failed to close writer", "error", err)
	}

	slog.Info("Server exiting gracefully")

}

func runEmailWorker(broker, topic string) {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{broker},
		Topic:   topic,
		GroupID: "email-sender-group",
	})
	defer reader.Close()

	slog.Info("Email worker started, waiting for messages...")

	for {
		msg, err := reader.ReadMessage(context.Background())
		if err != nil {
			slog.Error("worker read error", "error", err)
			break
		}
		slog.Info("[WORKER] Sending Welcome Email...",
			"event", string(msg.Value),
			"partition", msg.Partition,
			"offset", msg.Offset)
	}
}
