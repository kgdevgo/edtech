package worker

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/lib/pq"
	"github.com/segmentio/kafka-go"
)

type Relay struct {
	db          *sql.DB
	kafkaWriter *kafka.Writer
}

func NewRelay(db *sql.DB, kw *kafka.Writer) *Relay {
	return &Relay{db: db, kafkaWriter: kw}
}

func (r *Relay) Start(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	slog.Info("Outbox relay worker started")

	for {
		select {
		case <-ctx.Done():
			slog.Info("Outbox relay worker stopped")
			return
		case <-ticker.C:
			r.processOutbox(ctx)
		}
	}
}

func (r *Relay) processOutbox(ctx context.Context) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		slog.Error("relay: failed to begin tx", "error", err)
		return
	}
	defer func() {
		_ = tx.Rollback()
	}()

	query := `
		SELECT id, topic, payload
		FROM outbox_events
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT 100
		FOR UPDATE SKIP LOCKED
	`

	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		slog.Error("relay: failed to fetch events", "error", err)
		return
	}
	defer rows.Close()

	type event struct {
		id      string
		topic   string
		payload []byte
	}
	var events []event

	for rows.Next() {
		var e event
		if err := rows.Scan(&e.id, &e.topic, &e.payload); err != nil {
			slog.Error("relay: failed to scan event", "error", err)
			return
		}
		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		slog.Error("relay: rows iteration error", "error", err)
		return
	}

	if len(events) == 0 {
		return
	}

	var msgs []kafka.Message
	var ids []string
	for _, e := range events {
		msgs = append(msgs, kafka.Message{
			Topic: e.topic,
			Key:   []byte(e.id),
			Value: e.payload,
		})
		ids = append(ids, e.id)
	}

	kafkaCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	err = r.kafkaWriter.WriteMessages(kafkaCtx, msgs...)
	if err != nil {
		slog.Error("relay: failed to send batch to kafka", "error", err, "batch_size", len(msgs))
		return
	}

	_, err = tx.ExecContext(ctx, `UPDATE outbox_events SET status = 'done' WHERE id = ANY($1)`, pq.Array(ids))
	if err != nil {
		slog.Error("relay: failed to update statuses", "error", err)
		return
	}

	if err := tx.Commit(); err != nil {
		slog.Error("relay: failed to commit tx", "error", err)
	} else {
		slog.Info("relay: batch sent successfully", "count", len(ids))
	}
}
