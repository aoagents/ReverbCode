package domain

// ServiceError is the single error shape every service/ package returns and
// controllers translate into the locked HTTP APIError envelope. Carrying the
// Kind/Code/Message/Details inline lets a controller map any service error to a
// response with one errors.As — no per-resource sentinel imports or switches.
type ServiceError struct {
	Kind    string
	Code    string
	Message string
	Details map[string]any
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// NewServiceError builds a ServiceError from its parts.
func NewServiceError(kind, code, message string, details map[string]any) *ServiceError {
	return &ServiceError{Kind: kind, Code: code, Message: message, Details: details}
}

// BadRequestError is a 400-class service error.
func BadRequestError(code, message string, details map[string]any) *ServiceError {
	return NewServiceError(KindBadRequest, code, message, details)
}

// NotFoundError is a 404-class service error.
func NotFoundError(code, message string) *ServiceError {
	return NewServiceError(KindNotFound, code, message, nil)
}

// ConflictError is a 409-class service error.
func ConflictError(code, message string, details map[string]any) *ServiceError {
	return NewServiceError(KindConflict, code, message, details)
}

// InternalError is a 500-class service error.
func InternalError(code, message string) *ServiceError {
	return NewServiceError(KindInternal, code, message, nil)
}

// Service error kinds. They double as the APIError envelope "kind" field and
// drive the controller's HTTP status mapping.
const (
	KindBadRequest     = "bad_request"
	KindNotFound       = "not_found"
	KindConflict       = "conflict"
	KindInternal       = "internal"
	KindNotImplemented = "not_implemented"
)
