// Package domain holds shared vocabulary for sessions, activity, and PR facts.
// Session state is deliberately small: durable session rows carry activity_state
// plus an is_terminated bit; user-facing status is derived from those fields and
// PR facts at read time.
package domain
