package models

type Course struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
	Price    int    `json:"price"`
}

type Student struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Enrollment struct {
	StudentID string `json:"student_id"`
	CourseID  string `json:"course_id"`
}
