package handlers

import (
	"context"
	"edtech-pg/internal/models"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type MockStorage struct {
	courses []models.Course
}

func (m *MockStorage) CreateCourse(ctx context.Context, course *models.Course) error {
	return nil
}

func (m *MockStorage) CreateStudent(ctx context.Context, student *models.Student) error {
	return nil
}

func (m *MockStorage) Enroll(ctx context.Context, studentID, courseID string) error {
	return nil
}

func (m *MockStorage) GetAllCourses(ctx context.Context) ([]models.Course, error) {
	return m.courses, nil
}

func TestGetCourses(t *testing.T) {
	mockStore := &MockStorage{
		courses: []models.Course{
			{ID: "1", Title: "Go for Beginners", Duration: 10, Price: 10000},
			{ID: "2", Title: "Kubernetes Masterclass", Duration: 20, Price: 20000},
		},
	}

	handler := New(mockStore, nil)

	req, err := http.NewRequest("GET", "/courses", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()

	handler.GetCourses(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var responseCourses []models.Course
	if err := json.NewDecoder(rr.Body).Decode(&responseCourses); err != nil {
		t.Fatal("failed to decode response body")
	}

	if len(responseCourses) != 2 {
		t.Errorf("expected 2 courses, got %d", len(responseCourses))
	}

	if responseCourses[0].Title != "Go for Beginners" {
		t.Errorf("expected course title 'Go for Beginners', got '%s'", responseCourses[0].Title)
	}

	if responseCourses[1].Title != "Kubernetes Masterclass" {
		t.Errorf("expected course title 'Kubernetes Masterclass', got '%s'", responseCourses[1].Title)
	}
}
