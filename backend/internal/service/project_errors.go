package service

// ProjectError is the service-level error shape controllers translate into the
// locked HTTP APIError envelope without knowing store internals.
type ProjectError struct {
	Kind    string
	Code    string
	Message string
	Details map[string]any
}

func (e *ProjectError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func newProjectError(kind, code, message string, details map[string]any) *ProjectError {
	return &ProjectError{Kind: kind, Code: code, Message: message, Details: details}
}

func projectBadRequest(code, message string, details map[string]any) *ProjectError {
	return newProjectError("bad_request", code, message, details)
}

func projectNotFound(code, message string) *ProjectError {
	return newProjectError("not_found", code, message, nil)
}

func projectConflict(code, message string, details map[string]any) *ProjectError {
	return newProjectError("conflict", code, message, details)
}

func projectInternal(code, message string) *ProjectError {
	return newProjectError("internal", code, message, nil)
}
