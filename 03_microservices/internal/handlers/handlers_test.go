package handlers

import (
	"bytes"
	"context"
	"edtech-pg/internal/auth"
	"edtech-pg/internal/models"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type MockStorage struct {
	courses          []models.Course
	CreateStudentErr error
}

func (m *MockStorage) CreateCourse(ctx context.Context, course *models.Course) error {
	return nil
}

func (m *MockStorage) Enroll(ctx context.Context, studentID, courseID string) error {
	return nil
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
