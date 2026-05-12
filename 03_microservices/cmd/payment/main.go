package main

import (
	"context"
	"edtech-pg/internal/config"
	"edtech-pg/internal/events"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	slog.Info("Starting payment-service")

	cfg := config.Load()

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{cfg.Kafka.Broker},
		Topic:       events.TopicEnrollmentCreated,
		GroupID:     "payment-processor-group-v3",
		StartOffset: kafka.FirstOffset,
		MaxWait:     1 * time.Second,
	})
	defer reader.Close()

	writer := &kafka.Writer{
		Addr:                   kafka.TCP(cfg.Kafka.Broker),
		Topic:                  events.TopicPaymentCompleted,
		Balancer:               &kafka.LeastBytes{},
		AllowAutoTopicCreation: true,
	}
	defer writer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
		<-quit
		slog.Info("Shutting down payment-service...")
		cancel()
	}()

	slog.Info("Payment service waiting for payment commands...")

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				break
			}
			slog.Error("read error", "error", err)
			break
		}

		var cmd events.EnrollmentCreatedPayload
		if err = json.Unmarshal(msg.Value, &cmd); err != nil {
			slog.Error("[PAYMENT] Failed to parse payment command", "error", err)
			continue
		}

		slog.Info("[PAYMENT] Processing payment...", "student_id", cmd.StudentID, "event_id", cmd.EventID)

		time.Sleep(2 * time.Second)

		status := "success"
		if r.Float32() < 0.2 {
			status = "failed"
		}

		result := events.PaymentCompletedPayload{
			EventID:   cmd.EventID,
			StudentID: cmd.StudentID,
			CourseID:  cmd.CourseID,
			Status:    status,
		}

		resultBytes, _ := json.Marshal(result)

		var writeErr error
		for i := 0; i < 3; i++ {
			writeErr = writer.WriteMessages(ctx, kafka.Message{
				Key:   []byte(cmd.EventID),
				Value: resultBytes,
			})
			if writeErr == nil {
				break
			}
			slog.Warn("retrying write to kafka", "attempt", i+1, "error", writeErr)
			time.Sleep(500 * time.Millisecond)
		}

		if writeErr != nil {
			slog.Error("failed to write result", "error", writeErr)
			continue
		}

		slog.Info("[PAYMENT] Finished", "status", status, "student_id", cmd.StudentID)
	}

	slog.Info("Payment service stopped")
}
