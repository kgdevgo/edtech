package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/smtp"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	slog.Info("Starting edtech-worker...")

	// Configs
	kafkaBroker := getEnv("KAFKA_BROKER", "localhost:29092")
	topicName := "enrollments"

	smtpHost := getEnv("SMTP_HOST", "smtp.gmail.com")
	smtpPort := getEnv("SMTP_PORT", "587")
	smtpUser := getEnv("SMTP_USER", "")
	smtpPassword := getEnv("SMTP_PASSWORD", "")

	// Kafka Reader
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{kafkaBroker},
		Topic:   topicName,
		GroupID: "email-sender-group",
		MaxWait: 1 * time.Second,
	})
	defer reader.Close()

	smtpAuth := smtp.PlainAuth("", smtpUser, smtpPassword, smtpHost)
	smtpAddr := fmt.Sprintf("%s:%s", smtpHost, smtpPort)

	// Context & Graceful Shutdown Setup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
		<-quit
		slog.Info("Received termination signal. Shutting down Email Worker...")
		cancel()
	}()

	slog.Info("Email worker started, waiting for messages...")

	// Main Event Loop
	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				slog.Info("Email worker loop stopped cleanly")
				break
			}
			slog.Error("worker read error", "error", err)
			break
		}

		slog.Info("[WORKER] Processing event...", "offset", msg.Offset)

		to := []string{smtpUser}
		emailBody := fmt.Sprintf("Subject: New Enrollment\r\n\r\n"+
			"Hello, you have successfully enrolled in the course. New Kafka event. \n\nData (JSON): %s",
			string(msg.Value))

		err = smtp.SendMail(smtpAddr, smtpAuth, smtpUser, to, []byte(emailBody))
		if err != nil {
			slog.Error("[WORKER] Failed to send email", "error", err)
		} else {
			slog.Info("[WORKER] Email sent successfully", "to", smtpUser)
		}
	}

	time.Sleep(1 * time.Second)
	slog.Info("Email Worker stopped gracefully")
}
