package storage

import (
	"context"
	"database/sql"
	"edtech-pg/internal/models"
	"errors"
	"fmt"
	"time"

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

func (s *Storage) Enroll(ctx context.Context, studentID, courseID string) error {
	query := `INSERT INTO enrollments (student_id, course_id) VALUES ($1, $2)`
	_, err := s.db.ExecContext(ctx, query, studentID, courseID)
	return parseDBError(err)
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

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return courses, nil
}

func (s *Storage) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	return s.db.PingContext(ctx)
}
