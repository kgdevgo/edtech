package main

import (
	"context"
	"edtech-pg/internal/config"
	"errors"
	"log/slog"
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
		Brokers: []string{cfg.Kafka.Broker},
		Topic:   "payments_commands",
		GroupID: "payment-processor-group",
		MaxWait: 1 * time.Second,
	})
	defer reader.Close()

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

	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				break
			}
			slog.Error("read error", "error", err)
			break
		}

		slog.Info("[PAYMENT] Received payment request", "data", string(msg.Value))

		// TODO payment processing
		// and Kafka write
	}

	slog.Info("Payment service stopped")
}
