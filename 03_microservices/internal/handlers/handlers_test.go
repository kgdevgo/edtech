package handlers

import (
	"bytes"
	"context"
	"edtech-pg/internal/auth"
	"edtech-pg/internal/models"
	"edtech-pg/internal/storage"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type MockStorage struct {
	courses          []models.Course
	CreateStudentErr error
	EnrollErr        error
}

func (m *MockStorage) CreateCourse(ctx context.Context, course *models.Course) error {
	return nil
}

func (m *MockStorage) Enroll(ctx context.Context, studentID, courseID string) error {
	return m.EnrollErr
}

func (m *MockStorage) Ping(ctx context.Context) error {
	return nil
}

func (m *MockStorage) GetAllCourses(ctx context.Context) ([]models.Course, error) {
	return m.courses, nil
}

func (m *MockStorage) CreateStudent(ctx context.Context, student *models.Student) error {
	return m.CreateStudentErr
}

func (m *MockStorage) CheckStudentExists(ctx context.Context, id string) (bool, error) {
	return true, nil
}

func TestCreateStudent(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    string
		mockDBError    error
		expectedStatus int
	}{
		{
			name:           "Valid Request - Success",
			requestBody:    `{"name": "John", "email": "john@example.com"}`,
			mockDBError:    nil,
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "Invalid JSON - Bad Request",
			requestBody:    `{"name": "John", "email": }`,
			mockDBError:    nil,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "DB Failed - Internal Error",
			requestBody:    `{"name": "John", "email": "john@example.com"}`,
			mockDBError:    errors.New("database connection lost"),
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStore := &MockStorage{
				CreateStudentErr: tt.mockDBError,
			}
			tokenManager, _ := auth.NewTokenManager("test-secret-key")
			handler := New(mockStore, tokenManager)

			req, _ := http.NewRequest("POST", "/students", bytes.NewBufferString(tt.requestBody))
			rr := httptest.NewRecorder()

			handler.CreateStudent(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("handler returned wrong status code: got %v want %v", status, tt.expectedStatus)
			}
		})
	}
}

func TestEnroll(t *testing.T) {
	tests := []struct {
		name           string
		ctxUserID      string
		requestBody    string
		mockDBError    error
		expectedStatus int
	}{
		{
			name:           "Valid Request - Success",
			ctxUserID:      "student-123",
			requestBody:    `{"course_id": "course-456"}`,
			mockDBError:    nil,
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "Unauthorized - Missing Context User",
			ctxUserID:      "",
			requestBody:    `{"course_id": "course-456"}`,
			mockDBError:    nil,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Invalid JSON - Bad Request",
			ctxUserID:      "student-123",
			requestBody:    `{"course_id": }`,
			mockDBError:    nil,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "DB Failed - Internal Error",
			ctxUserID:      "student-123",
			requestBody:    `{"course_id": "course-456"}`,
			mockDBError:    errors.New("database connection lost"),
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "Duplicate Enrollment - Conflict",
			ctxUserID:      "student-123",
			requestBody:    `{"course_id": "course-456"}`,
			mockDBError:    storage.ErrDuplicate,
			expectedStatus: http.StatusConflict,
		},
		{
			name:           "Invalid Course ID - Bad Request",
			ctxUserID:      "student-123",
			requestBody:    `{"course_id": "fake-course"}`,
			mockDBError:    storage.ErrForeignKey,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStore := &MockStorage{
				EnrollErr: tt.mockDBError,
			}
			tokenManager, _ := auth.NewTokenManager("test-secret-key")
			handler := New(mockStore, tokenManager)

			req := httptest.NewRequest(http.MethodPost, "/enroll", bytes.NewBufferString(tt.requestBody))

			if tt.ctxUserID != "" {
				ctx := context.WithValue(req.Context(), userIDKey, tt.ctxUserID)
				req = req.WithContext(ctx)
			}

			rr := httptest.NewRecorder()
			handler.Enroll(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("handler returned wrong status code: got %v want %v", status, tt.expectedStatus)
			}

		})
	}
}
