package worker

import (
	"context"
	"edtech-pg/internal/events"
	"encoding/json"
	"log/slog"

	"github.com/segmentio/kafka-go"
)

type ResultProcessor interface {
	ProcessPaymentResult(ctx context.Context, studentID, courseID, paymentStatus string) error
}

type Consumer struct {
	reader *kafka.Reader
	db     ResultProcessor
}

func NewConsumer(reader *kafka.Reader, db ResultProcessor) *Consumer {
	return &Consumer{reader: reader, db: db}
}

func (c *Consumer) Start(ctx context.Context) {
	slog.Info("API Result Consumer started")

	for {
		msg, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("Consumer stopping due to context cancellation")
				return
			}
			slog.Error("consumer read error", "error", err)
			continue
		}

		var result events.PaymentCompletedPayload
		if err := json.Unmarshal(msg.Value, &result); err != nil {
			slog.Error("failed to unmarshal payment result", "error", err)
			continue
		}

		slog.Info("Received payment result", "student_id", result.StudentID, "status", result.Status)

		if err := c.db.ProcessPaymentResult(ctx, result.StudentID, result.CourseID, result.Status); err != nil {
			slog.Error("failed to process payment result in db", "error", err)
		}
	}
}
