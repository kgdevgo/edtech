package main

import (
	"context"
	"edtech-pg/internal/config"
	"edtech-pg/internal/events"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/segmentio/kafka-go"
)

var (
	dlqTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "edtech_payment_dlq_messages_total",
			Help: "Total number of payment messages routed to Dead Letter Queue",
		},
	)
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
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

	dlqWriter := &kafka.Writer{
		Addr:                   kafka.TCP(cfg.Kafka.Broker),
		Topic:                  events.TopicEnrollmentCreated + ".dlq",
		Balancer:               &kafka.LeastBytes{},
		AllowAutoTopicCreation: true,
	}
	defer dlqWriter.Close()

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
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				break
			}
			slog.Error("read error", "error", err)
			break
		}

		var cmd events.EnrollmentCreatedPayload
		if err = json.Unmarshal(msg.Value, &cmd); err != nil {
			slog.Error("[PAYMENT] Failed to parse payment command, routing to DLQ", "error", err)
			routeToDLQ(ctx, dlqWriter, msg, fmt.Sprintf("unmarshal error: %v", err))
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		log := slog.With("correlation_id", cmd.CorrelationID)

		if cmd.EventID == "" || cmd.StudentID == "" {
			log.Error("[PAYMENT] Missing event_id or student_id, routing to DLQ")
			routeToDLQ(ctx, dlqWriter, msg, "missing event_id or student_id")
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		log.Info("[PAYMENT] Processing payment...", "student_id", cmd.StudentID, "event_id", cmd.EventID)

		time.Sleep(2 * time.Second)

		status := "success"
		if r.Float32() < 0.2 {
			status = "failed"
		}

		result := events.PaymentCompletedPayload{
			EventID:       cmd.EventID,
			CorrelationID: cmd.CorrelationID,
			StudentID:     cmd.StudentID,
			CourseID:      cmd.CourseID,
			Status:        status,
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
			log.Warn("retrying write to kafka", "attempt", i+1, "error", writeErr)
			time.Sleep(500 * time.Millisecond)
		}

		if writeErr != nil {
			log.Error("failed to write result", "error", writeErr)
			continue
		}

		log.Info("[PAYMENT] Finished", "status", status, "student_id", cmd.StudentID)

		if err := reader.CommitMessages(ctx, msg); err != nil {
			log.Error("[PAYMENT] Failed to commit message", "error", err)
		}
	}

	slog.Info("Payment service stopped")
}

func routeToDLQ(ctx context.Context, writer *kafka.Writer, originalMsg kafka.Message, reason string) {
	dlqMsg := kafka.Message{
		Key:   originalMsg.Key,
		Value: originalMsg.Value,
		Headers: append(originalMsg.Headers, kafka.Header{
			Key:   "dlq_reason",
			Value: []byte(reason),
		}),
	}

	if err := writer.WriteMessages(ctx, dlqMsg); err != nil {
		slog.Error("CRITICAL: Failed to write to DLQ", "error", err)
	} else {
		slog.Info("Message successfully routed to DLQ", "reason", reason)
		dlqTotal.Inc()
	}
}
