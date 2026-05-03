package storage_test

import (
	"context"
	"database/sql"
	"edtech-pg/internal/models"
	"edtech-pg/internal/storage"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func TestPostgresContainerStarts(t *testing.T) {
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
		t.Fatalf("failed to start container: %s", err)
	}

	defer func() {
		if err = pgContainer.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate container: %s", err)
		}
	}()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get database connection string: %s", err)
	}

	m, err := migrate.New("file://../../migrations", connStr)
	if err != nil {
		t.Fatalf("failed to create migration instance: %s", err)
	}
	defer m.Close()

	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("failed to run migrations: %s", err)
	}

	t.Log("Migrations run successfully")

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("failed to open database connection: %s", err)
	}
	defer db.Close()

	repo := storage.New(db)

	testStudent := models.Student{
		ID:    "11111111-1111-1111-1111-111111111111",
		Name:  "Test Student",
		Email: "test@test.com",
	}

	testCourse := models.Course{
		ID:       "22222222-2222-2222-2222-222222222222",
		Title:    "Test Course",
		Duration: 120,
		Price:    15000,
	}

	_, err = db.Exec(`INSERT INTO students (id, name, email) VALUES ($1, $2, $3)`,
		testStudent.ID,
		testStudent.Name,
		testStudent.Email,
	)
	if err != nil {
		t.Fatalf("failed to create student: %s", err)
	}

	_, err = db.Exec(`INSERT INTO courses (id, title, duration, price) VALUES ($1, $2, $3, $4)`,
		testCourse.ID,
		testCourse.Title,
		testCourse.Duration,
		testCourse.Price,
	)
	if err != nil {
		t.Fatalf("failed to create course: %s", err)
	}

	err = repo.Enroll(ctx, testStudent.ID, testCourse.ID)
	if err != nil {
		t.Fatalf("failed to enroll student: %s", err)
	}

	var outboxCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE topic = 'enrollments'`).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("failed to query outbox events: %s", err)
	}

	if outboxCount != 1 {
		t.Fatalf("expected 1 outbox event, got %d", outboxCount)
	}

	t.Log("Successfully enrolled student")
}
