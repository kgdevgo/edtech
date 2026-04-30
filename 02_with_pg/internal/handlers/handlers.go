package handlers

import (
	"context"
	"edtech-pg/internal/models"
	"edtech-pg/internal/storage"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	enrollmentsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "edtech_enrollments_total",
			Help: "Total number of successful enrollments",
		},
	)
)

type EdtechRepository interface {
	CreateCourse(ctx context.Context, course *models.Course) error
	GetAllCourses(ctx context.Context) ([]models.Course, error)
	CreateStudent(ctx context.Context, student *models.Student) error
	Enroll(ctx context.Context, studentID, courseID string) error
	Ping(ctx context.Context) error

	CheckStudentExists(ctx context.Context, id string) (bool, error)
}

type TokenProvider interface {
	GenerateToken(studentID string) (string, error)
	ParseToken(tokenString string) (string, error)
}

type Handler struct {
	store EdtechRepository
	token TokenProvider
}

func New(store EdtechRepository, token TokenProvider) *Handler {

	return &Handler{store: store, token: token}
}

func TimeoutMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StudentID string `json:"student_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	exists, err := h.store.CheckStudentExists(r.Context(), req.StudentID)
	if err != nil {
		slog.Error("database error", "op", "CheckStudentExists", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if !exists {
		slog.Warn("failed login attempt: student not found", "id", req.StudentID)
		http.Error(w, "Student not found", http.StatusNotFound)
		return
	}

	token, err := h.token.GenerateToken(req.StudentID)
	if err != nil {
		slog.Error("failed to generate token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (h *Handler) CreateCourse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var course models.Course

	if err := json.NewDecoder(r.Body).Decode(&course); err != nil {
		slog.Error("failed to decode json", "error", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	course.ID = uuid.New().String()

	if err := h.store.CreateCourse(ctx, &course); err != nil {
		slog.Error("database error", "op", "CreateCourse", "error", err)

		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "Database timeout", http.StatusGatewayTimeout)
			return
		}
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	slog.Info("course created", "id", course.ID, "title", course.Title)
	respondJSON(w, http.StatusCreated, course)
}

func (h *Handler) CreateStudent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var student models.Student

	if err := json.NewDecoder(r.Body).Decode(&student); err != nil {
		slog.Error("failed to decode json", "error", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	student.ID = uuid.New().String()

	if err := h.store.CreateStudent(ctx, &student); err != nil {
		slog.Error("database error", "op", "CreateStudent", "error", err)

		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "Database timeout", http.StatusGatewayTimeout)
			return
		}
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	slog.Info("student created", "id", student.ID)
	respondJSON(w, http.StatusCreated, student)
}

func (h *Handler) Enroll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	studentID, ok := ctx.Value(userIDKey).(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		CourseID string `json:"course_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("failed to decode json", "error", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	slog.Info("debug enroll", "parsed_student_id", studentID, "parsed_course_id", req.CourseID)

	if err := h.store.Enroll(ctx, studentID, req.CourseID); err != nil {
		slog.Error("database error", "op", "Enroll", "error", err)

		if errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "Database timeout", http.StatusGatewayTimeout)
			return
		}
		if errors.Is(err, storage.ErrDuplicate) {
			http.Error(w, "Student is already enrolled in this course", http.StatusConflict)
			return
		}
		if errors.Is(err, storage.ErrForeignKey) {
			http.Error(w, "Student or Course does not exist", http.StatusBadRequest)
			return
		}

		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusCreated, map[string]string{"status": "successfully enrolled"})
	enrollmentsTotal.Inc()
}

func (h *Handler) GetCourses(w http.ResponseWriter, r *http.Request) {
	courses, err := h.store.GetAllCourses(r.Context())
	if err != nil {
		slog.Error("Failed to fetch courses", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, courses)
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		slog.Warn("client disconnected before healthcheck response was sent", "error", err)
	}
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Ping(r.Context()); err != nil {
		slog.Error("readiness probe failed: database unreachable", "error", err)
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		slog.Warn("client disconnected before readycheck response was sent", "error", err)
	}
}
