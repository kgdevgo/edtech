package events

type EnrollmentCreatedPayload struct {
	StudentID string `json:"student_id"`
	CourseID  string `json:"course_id"`
}

type PaymentCompletedPayload struct {
	StudentID string `json:"student_id"`
	CourseID  string `json:"course_id"`
	Status    string `json:"status"`
}
