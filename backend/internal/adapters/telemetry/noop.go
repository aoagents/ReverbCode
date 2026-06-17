package telemetry

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// NoopSink discards every event.
type NoopSink struct{}

func (NoopSink) Emit(context.Context, ports.TelemetryEvent) {}

func (NoopSink) Close(context.Context) error { return nil }
