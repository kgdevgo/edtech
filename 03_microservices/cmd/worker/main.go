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

	"edtech-pg/internal/config"

	"github.com/segmentio/kafka-go"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	slog.Info("Starting edtech-worker...")

	// Config
	cfg := config.Load()

	// Kafka Reader
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{cfg.Kafka.Broker},
		Topic:   cfg.Kafka.Topic,
		GroupID: "email-sender-group",
		MaxWait: 1 * time.Second,
	})
	defer reader.Close()

	smtpAuth := smtp.PlainAuth("", cfg.SMTP.User, cfg.SMTP.Password, cfg.SMTP.Host)
	smtpAddr := fmt.Sprintf("%s:%s", cfg.SMTP.Host, cfg.SMTP.Port)

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

		to := []string{cfg.SMTP.User}
		emailBody := fmt.Sprintf("Subject: New Enrollment\r\n\r\n"+
			"Hello, you have successfully enrolled in the course. New Kafka event. \n\nData (JSON): %s",
			string(msg.Value))

		err = smtp.SendMail(smtpAddr, smtpAuth, cfg.SMTP.User, to, []byte(emailBody))
		if err != nil {
			slog.Error("[WORKER] Failed to send email", "error", err)
		} else {
			slog.Info("[WORKER] Email sent successfully", "to", cfg.SMTP.User)
		}
	}

	time.Sleep(1 * time.Second)
	slog.Info("Email Worker stopped gracefully")
}
