package project

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// Error is the project service's error shape. It is an alias for the shared
// domain.ServiceError so every service/ package speaks one error language and
// controllers translate them all with a single errors.As.
type Error = domain.ServiceError

func badRequest(code, message string, details map[string]any) *Error {
	return domain.BadRequestError(code, message, details)
}

func notFound(code, message string) *Error {
	return domain.NotFoundError(code, message)
}

func conflict(code, message string, details map[string]any) *Error {
	return domain.ConflictError(code, message, details)
}

func internal(code, message string) *Error {
	return domain.InternalError(code, message)
}
