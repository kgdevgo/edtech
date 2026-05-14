package events

type EnrollmentCreatedPayload struct {
	EventID       string `json:"event_id"`
	CorrelationID string `json:"correlation_id"`
	StudentID     string `json:"student_id"`
	CourseID      string `json:"course_id"`
}

type PaymentCompletedPayload struct {
	EventID       string `json:"event_id"`
	CorrelationID string `json:"correlation_id"`
	StudentID     string `json:"student_id"`
	CourseID      string `json:"course_id"`
	Status        string `json:"status"`
}
