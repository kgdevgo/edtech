package worker

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

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

	var id, topic string
	var payload []byte

	query := `
		SELECT id, topic, payload
		FROM outbox_events
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`

	err = tx.QueryRowContext(ctx, query).Scan(&id, &topic, &payload)
	if err != nil {
		if err != sql.ErrNoRows {
			slog.Error("relay: failed to fetch event", "error", err)
		}
		return
	}

	kafkaCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	err = r.kafkaWriter.WriteMessages(kafkaCtx, kafka.Message{
		Key:   []byte(id),
		Value: payload,
	})

	if err != nil {
		slog.Error("relay: failed to send to kafka", "error", err, "event_id", id)
		return
	}

	_, err = tx.ExecContext(ctx, `UPDATE outbox_events SET status = 'done' WHERE id = $1`, id)
	if err != nil {
		slog.Error("relay: failed to update status", "error", err, "event_id", id)
		return
	}

	if err := tx.Commit(); err != nil {
		slog.Error("relay: failed to commit tx", "error", err)
	} else {
		slog.Info("relay: event sent successfully", "event_id", id)
	}
}
