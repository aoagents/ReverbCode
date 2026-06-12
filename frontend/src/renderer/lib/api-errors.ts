/**
 * openapi-fetch resolves non-2xx responses to a plain APIError envelope
 * ({ code, error, message, ... }), not an Error — String() on it renders
 * "[object Object]". This normalizes either shape into a readable message.
 */
export function apiErrorMessage(error: unknown, fallback: string): string {
	if (error instanceof Error) return error.message;
	if (typeof error === "string" && error) return error;
	if (error && typeof error === "object") {
		const envelope = error as { message?: unknown; code?: unknown };
		if (typeof envelope.message === "string" && envelope.message) {
			return typeof envelope.code === "string" && envelope.code
				? `${envelope.message} (${envelope.code})`
				: envelope.message;
		}
	}
	return fallback;
}
