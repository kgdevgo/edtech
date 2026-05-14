package storage_test

import (
	"context"
	"database/sql"
	"edtech-pg/internal/events"
	"edtech-pg/internal/storage"
	"errors"
	"log"
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	testDB *sql.DB
	repo   *storage.Storage
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("test_db"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_pass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Second),
		),
	)
	if err != nil {
		log.Fatalf("failed to start container: %s", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("failed to get db connection string: %s", err)
	}
	// Apply migrations
	migrator, err := migrate.New("file://../../migrations", connStr)
	if err != nil {
		log.Fatalf("failed to create migrator: %s", err)
	}
	if err = migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatalf("failed to apply migrations: %s", err)
	}
	migrator.Close()

	// DB & repo initialization
	testDB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("failed to open db: %s", err)
	}
	repo = storage.New(testDB)

	// Run tests
	code := m.Run()

	// Cleanup
	testDB.Close()
	if err := pgContainer.Terminate(ctx); err != nil {
		log.Fatalf("failed to terminate container: %s", err)
	}

	os.Exit(code)
}

func TestEnroll(t *testing.T) {
	ctx := context.Background()

	studentID := uuid.New().String()
	courseID := uuid.New().String()

	_, err := testDB.Exec(`INSERT INTO students (id, name, email) VALUES ($1, $2, $3)`,
		studentID, "Test Student", "test1@test.com")
	if err != nil {
		t.Fatalf("failed to create student: %s", err)
	}

	_, err = testDB.Exec(`INSERT INTO courses (id, title, duration, price) VALUES ($1, $2, $3, $4)`,
		courseID, "Test Course", 120, 15000)
	if err != nil {
		t.Fatalf("failed to create course: %s", err)
	}

	err = repo.Enroll(ctx, "test-corr-id", studentID, courseID)
	if err != nil {
		t.Fatalf("failed to enroll student: %s", err)
	}

	var outboxCount int

	err = testDB.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE topic = $1`,
		events.TopicEnrollmentCreated).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("failed to query outbox events: %s", err)
	}

	if outboxCount != 1 {
		t.Fatalf("expected 1 outbox event, got %d", outboxCount)
	}
}

func TestProcessPaymentResult_Idempotency(t *testing.T) {
	ctx := context.Background()

	studentID := uuid.New().String()
	courseID := uuid.New().String()
	eventID := uuid.New().String()

	// Create student and course
	_, _ = testDB.Exec(`INSERT INTO students (id, name, email) VALUES ($1, $2, $3)`,
		studentID, "Idempotency Test", "idempotency@test.com")
	_, _ = testDB.Exec(`INSERT INTO courses (id, title, duration, price) VALUES ($1, $2, $3, $4)`,
		courseID, "Idempotency Course", 10, 500)
	_ = repo.Enroll(ctx, "test-corr-id", studentID, courseID)

	// First call
	err := repo.ProcessPaymentResult(ctx, eventID, "test-corr-id", studentID, courseID, "success")
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}

	// Second call (should be ignored)
	err = repo.ProcessPaymentResult(ctx, eventID, "test-corr-id", studentID, courseID, "success")
	if err != nil {
		t.Fatalf("Second call failed (should be ignored, not error): %v", err)
	}

	var count int
	err = testDB.QueryRow(`
SELECT COUNT(*) FROM outbox_events
WHERE payload::text LIKE '%' || $1 || '%'`, studentID).Scan(&count)

	if err != nil {
		t.Fatalf("failed to count outbox events: %v", err)
	}

	if count != 2 {
		t.Errorf(
			"Expected exactly 2 outbox events for this student (Enroll + 1st Process)"+
				", got %d. Idempotency failed!",
			count)
	}
}
