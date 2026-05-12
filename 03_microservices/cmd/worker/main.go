package main

import (
	"context"
	"database/sql"
	"edtech-pg/internal/events"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/smtp"
	"os"
	"os/signal"
	"syscall"
	"time"

	"edtech-pg/internal/config"

	_ "github.com/lib/pq"
	"github.com/segmentio/kafka-go"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	slog.Info("Starting email-worker...")

	cfg := config.Load()

	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.DB.Host, cfg.DB.Port, cfg.DB.User, cfg.DB.Password, cfg.DB.Name)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Fatal("Failed to connect to database in email-worker", err)
	}
	slog.Info("Email worker successfully connected to Database!")

	// Kafka Reader
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{cfg.Kafka.Broker},
		Topic:       events.TopicEnrollmentActive,
		GroupID:     "email-sender-group-v2",
		StartOffset: kafka.FirstOffset,
		MaxWait:     1 * time.Second,
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
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				slog.Info("Email worker loop stopped cleanly")
				break
			}
			slog.Error("worker read error", "error", err)
			break
		}

		slog.Info("[WORKER] Processing event...", "offset", msg.Offset)

		var payload map[string]string
		if err := json.Unmarshal(msg.Value, &payload); err != nil {
			slog.Error("failed to unmarshal payload", "error", err)
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		eventID := payload["event_id"]
		courseID := payload["course_id"]
		if courseID == "" {
			courseID = "Unknown Course"
		}

		if eventID == "" {
			slog.Error("[WORKER] EventID is empty, rejecting message to maintain idempotency invariant")
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		res, err := db.ExecContext(
			ctx, `INSERT INTO processed_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING`,
			eventID,
		)
		if err != nil {
			slog.Error("[WORKER] Failed to check idempotency", "error", err)
			continue
		}

		affected, _ := res.RowsAffected()
		if affected == 0 {
			slog.Info(
				"[WORKER] Duplicate event detected, email already sent. Skipping.",
				"event_id",
				eventID,
			)
			continue
		}

		slog.Info("[WORKER] Sending email...", "event_id", eventID)

		to := []string{cfg.SMTP.User}
		emailBody := fmt.Sprintf("Subject: New Enrollment\r\n\r\n"+
			"Hello, you have successfully enrolled in the course: %s. \n\n New Kafka event. \n\nData (JSON): %s",
			courseID, string(msg.Value))

		done := make(chan error, 1)
		go func() {
			done <- smtp.SendMail(smtpAddr, smtpAuth, cfg.SMTP.User, to, []byte(emailBody))
		}()

		select {
		case <-time.After(5 * time.Second):
			slog.Error("[WORKER] SMTP connection timed out. NOT commiting offset.")
		case err := <-done:
			if err != nil {
				slog.Error("[WORKER] Failed to send email", "error", err)
			} else {
				slog.Info("[WORKER] Email sent successfully", "to", cfg.SMTP.User)
				if err := reader.CommitMessages(ctx, msg); err != nil {
					slog.Error("[WORKER] Failed to commit message", "error", err)
				}
			}
		}
	}

	time.Sleep(1 * time.Second)
	slog.Info("Email Worker stopped gracefully")
}
