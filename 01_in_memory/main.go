package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
)

type Course struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
	Price    int    `json:"price"`
}

type Student struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Email   string   `json:"email"`
	Courses []string `json:"courses"`
}

type Enrollment struct {
	StudentID string `json:"student_id"`
	CourseID  string `json:"course_id"`
}

var (
	courseStore  = make(map[string]Course)
	storeMutex   sync.RWMutex
	studentStore = make(map[string]Student)
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /courses", createCourseHandler)
	mux.HandleFunc("GET /courses", getCoursesHandler)
	mux.HandleFunc("POST /students", createStudentHandler)
	mux.HandleFunc("POST /enroll", enrollHandler)

	fmt.Println("Server started on http://localhost:8080")

	err := http.ListenAndServe(":8080", mux)
	if err != nil {
		log.Fatal(err)
	}

}

func enrollHandler(w http.ResponseWriter, r *http.Request) {
	var enroll Enrollment

	err := json.NewDecoder(r.Body).Decode(&enroll)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	storeMutex.Lock()
	defer storeMutex.Unlock()

	student, studentExists := studentStore[enroll.StudentID]
	if !studentExists {
		http.Error(w, "Student not found", http.StatusNotFound)
		return
	}

	_, courseExists := courseStore[enroll.CourseID]
	if !courseExists {
		http.Error(w, "Course not found", http.StatusNotFound)
		return
	}

	student.Courses = append(student.Courses, enroll.CourseID)
	studentStore[enroll.StudentID] = student

	w.WriteHeader(http.StatusCreated)
	err = json.NewEncoder(w).Encode(student)
	if err != nil {
		return
	}
}

func createStudentHandler(w http.ResponseWriter, r *http.Request) {
	var student Student

	if err := decodeJSON(r, &student); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	storeMutex.Lock()
	studentStore[student.ID] = student
	storeMutex.Unlock()

	respondJSON(w, http.StatusCreated, student)

}

func createCourseHandler(w http.ResponseWriter, r *http.Request) {
	var course Course

	if err := decodeJSON(r, &course); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	storeMutex.Lock()
	courseStore[course.ID] = course
	storeMutex.Unlock()

	respondJSON(w, http.StatusCreated, course)
}

func getCoursesHandler(w http.ResponseWriter, r *http.Request) {
	storeMutex.RLock()
	courses := make([]Course, 0, len(courseStore))
	for _, course := range courseStore {
		courses = append(courses, course)
	}

	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(courses)
	if err != nil {
		return
	}
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
