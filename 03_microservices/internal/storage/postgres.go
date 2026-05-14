package storage

import (
	"context"
	"database/sql"
	"edtech-pg/internal/events"
	"edtech-pg/internal/models"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrDuplicate  = errors.New("record already exists")
	ErrForeignKey = errors.New("foreign key violation (parent record missing)")
)

type Storage struct {
	db *sql.DB
}

func New(db *sql.DB) *Storage {
	return &Storage{db: db}
}

func parseDBError(err error) error {
	if err == nil {
		return nil
	}

	if pqErr, ok := errors.AsType[*pq.Error](err); ok {
		switch pqErr.Code {
		case "23505":
			return ErrDuplicate
		case "23503":
			return ErrForeignKey
		}
	}
	return err
}

func insertOutboxEvent(ctx context.Context, tx *sql.Tx, eventID, topic string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}

	query := `INSERT INTO outbox_events (id, topic, payload, status) VALUES ($1, $2, $3, 'pending')`
	_, err = tx.ExecContext(ctx, query, eventID, topic, data)
	return err
}

func (s *Storage) CreateCourse(ctx context.Context, course *models.Course) error {
	query := `INSERT INTO courses (id, title, duration, price) VALUES ($1, $2, $3, $4)`
	_, err := s.db.ExecContext(ctx, query, course.ID, course.Title, course.Duration, course.Price)
	return parseDBError(err)
}

func (s *Storage) CreateStudent(ctx context.Context, student *models.Student) error {
	query := `INSERT INTO students (id, name, email) VALUES ($1, $2, $3)`
	_, err := s.db.ExecContext(ctx, query, student.ID, student.Name, student.Email)
	return parseDBError(err)
}

func (s *Storage) CheckStudentExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM students WHERE id = $1)`

	err := s.db.QueryRowContext(ctx, query, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("storage: check student exists failed: %w", err)
	}

	return exists, nil
}

func (s *Storage) Enroll(ctx context.Context, corrID, studentID, courseID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	enrollQuery := `INSERT INTO enrollments (student_id, course_id) VALUES ($1, $2)`
	_, err = tx.ExecContext(ctx, enrollQuery, studentID, courseID)
	if err != nil {
		return parseDBError(err)
	}

	eventID := uuid.New().String()
	payload := events.EnrollmentCreatedPayload{
		EventID:       eventID,
		CorrelationID: corrID,
		StudentID:     studentID,
		CourseID:      courseID,
	}

	if err := insertOutboxEvent(ctx, tx, eventID, events.TopicEnrollmentCreated, payload); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

func (s *Storage) GetAllCourses(ctx context.Context) ([]models.Course, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, duration, price FROM courses`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var courses []models.Course
	for rows.Next() {
		var c models.Course
		if err := rows.Scan(&c.ID, &c.Title, &c.Duration, &c.Price); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		courses = append(courses, c)
	}

	return courses, rows.Err()
}

func (s *Storage) ProcessPaymentResult(ctx context.Context, eventID, corrID, studentID, courseID, paymentStatus string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(
		ctx,
		`INSERT INTO processed_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING`,
		eventID,
	)
	if err != nil {
		return parseDBError(err)
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil
	}

	newStatus := "active"
	if paymentStatus == "failed" {
		newStatus = "canceled"
	}

	query := `UPDATE enrollments SET status = $1 WHERE student_id = $2 AND course_id = $3 AND status = 'pending'`
	_, err = tx.ExecContext(ctx, query, newStatus, studentID, courseID)
	if err != nil {
		return parseDBError(err)
	}

	if newStatus == "active" {
		emailEventID := uuid.New().String()
		payload := map[string]string{
			"event_id":       emailEventID,
			"correlation_id": corrID,
			"student_id":     studentID,
			"course_id":      courseID,
		}
		if err := insertOutboxEvent(ctx, tx, emailEventID, events.TopicEnrollmentActive, payload); err != nil {
			return err
		}
	}

	return tx.Commit()

}

func (s *Storage) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	return s.db.PingContext(ctx)
}
