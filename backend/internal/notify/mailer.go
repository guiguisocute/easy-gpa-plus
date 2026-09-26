// Package notify owns outbound message delivery. Business packages depend only
// on Mailer, so provider changes never enter authentication or review logic.
package notify

import "context"

// NotSubmittedError means validation failed before any request reached SES.
type NotSubmittedError struct{ Err error }

func (e *NotSubmittedError) Error() string { return e.Err.Error() }
func (e *NotSubmittedError) Unwrap() error { return e.Err }

type Message struct {
	// Delivery metadata is stored separately from rendered mail content.
	provider string // Selected once by the audited delivery pipeline.
	ClassID  int64
	EventID  string
	To       string
	Subject  string
	Text     string
	HTML     string
	Template string
	Data     map[string]any
	// Set only by the preference-aware queue for an explicitly selected
	// single-event reminder. This is never part of SES template data.
	Frequent bool
}

type Mailer interface {
	Send(context.Context, Message) (messageID string, err error)
}
