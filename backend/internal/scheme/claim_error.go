package scheme

// ClaimError separates a stable translation key and values from the default
// user-facing message. Callers must not classify errors by matching prose.
type ClaimError struct {
	Code    string
	Message string
	Params  map[string]any
}

func (e *ClaimError) Error() string { return e.Message }

func claimError(code, message string, params map[string]any) error {
	return &ClaimError{Code: code, Message: message, Params: params}
}
