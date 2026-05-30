// Package reaper implements the OBSERVE-layer polling timer that supplies the
// LCM with the two facts the LCM cannot wake itself to discover: a periodic
// duration-based escalation heartbeat, and per-session runtime liveness probes.
//
// The reaper sits OUTSIDE the LCM's per-session serial loop. It only REPORTS
// facts — it never decides whether a session is "truly" dead. The decider
// (anti-flap Detecting quarantine, terminal-session rules) is owned by the LCM
// and consumes these facts through the regular ApplyRuntimeObservation entry
// point. A probe error is reported as a probe-failure fact, never collapsed to
// "alive" or "dead", so the LCM's failed-probe ≠ dead invariant holds.
package reaper

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DefaultTickInterval is the cadence used when Config.Tick is zero. It mirrors
// the design doc's 5s sampling window for runtime liveness.
const DefaultTickInterval = 5 * time.Second

// RuntimeRegistry resolves a runtime adapter by the RuntimeName recorded in a
// session's RuntimeHandle. The reaper looks the runtime up per-session so a
// single reaper instance can probe tmux- and zellij-backed sessions side by
// side without knowing about either at construction.
type RuntimeRegistry interface {
	Runtime(name string) (ports.Runtime, bool)
}

// MapRegistry is the trivial RuntimeRegistry: a name->runtime map. Callers
// that need dynamic registration can implement RuntimeRegistry themselves.
type MapRegistry map[string]ports.Runtime

// Runtime implements RuntimeRegistry.
func (m MapRegistry) Runtime(name string) (ports.Runtime, bool) {
	rt, ok := m[name]
	return rt, ok
}

// Config holds the externally-tunable knobs for a Reaper. Every field is
// optional; zero values fall back to safe defaults so production wiring (which
// only needs to inject the LCM and registry) and tests (which inject a clock
// plus a fast tick) can both stay terse.
type Config struct {
	// Tick is the interval between ticks. <=0 means DefaultTickInterval.
	Tick time.Duration
	// Clock supplies ObservedAt and TickEscalations now stamps. nil means
	// time.Now. Injected in tests so assertions don't race wallclock.
	Clock func() time.Time
	// Logger receives operational diagnostics (probe errors, skipped sessions,
	// LCM call failures). The reaper logs but does not propagate these errors
	// because a single failed probe must not kill the loop. nil means
	// slog.Default.
	Logger *slog.Logger
}

// Reaper is the polling timer. Construct it with New; start the background
// goroutine with Start, or drive a single cycle synchronously with Tick.
type Reaper struct {
	lcm      ports.LifecycleManager
	registry RuntimeRegistry
	tick     time.Duration
	clock    func() time.Time
	logger   *slog.Logger
}

// New constructs a Reaper. The LCM is the sole writer destination (the reaper
// reports facts via ApplyRuntimeObservation and TickEscalations); the registry
// resolves the runtime adapter to use per session.
func New(lcm ports.LifecycleManager, registry RuntimeRegistry, cfg Config) *Reaper {
	r := &Reaper{
		lcm:      lcm,
		registry: registry,
		tick:     cfg.Tick,
		clock:    cfg.Clock,
		logger:   cfg.Logger,
	}
	if r.tick <= 0 {
		r.tick = DefaultTickInterval
	}
	if r.clock == nil {
		r.clock = time.Now
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	return r
}

// Start launches the background goroutine and returns a channel that closes
// once the loop has exited. The loop exits on ctx cancellation; the channel
// gives the daemon a clean shutdown hook (wait on it after cancel to confirm
// the reaper has stopped before tearing down dependencies).
func (r *Reaper) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go r.loop(ctx, done)
	return done
}

func (r *Reaper) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(r.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.Tick(ctx); err != nil {
				r.logger.Error("reaper: tick failed", "err", err)
			}
		}
	}
}

// Tick runs one observation cycle: it always fires TickEscalations first (the
// duration-based escalation heartbeat, which the synchronous LCM cannot wake
// itself to drive), then enumerates the LCM's running sessions, probes each
// one's runtime, and reports any non-alive result back as a fact.
//
// Tick is exported so the daemon (and tests) can drive cycles synchronously,
// and so the Start goroutine has a single chokepoint to log against.
//
// Errors: only the RunningSessions failure is propagated, since it short-
// circuits the rest of the cycle. TickEscalations and per-session
// ApplyRuntimeObservation failures are logged but never propagated — one
// failed call must not bring down the loop.
func (r *Reaper) Tick(ctx context.Context) error {
	now := r.clock()

	// Heartbeat is best-effort and runs before enumeration so duration-based
	// escalations still fire if the running-set lookup is the thing that
	// errored. The LCM's TickEscalations is itself idempotent (no canonical
	// writes) — at worst we miss escalating once and pick it up next tick.
	if err := r.lcm.TickEscalations(ctx, now); err != nil {
		r.logger.Error("reaper: TickEscalations failed", "err", err)
	}

	sessions, err := r.lcm.RunningSessions(ctx)
	if err != nil {
		return err
	}

	for _, sess := range sessions {
		r.probeOne(ctx, sess, now)
	}
	return nil
}

// probeOne handles a single session's probe + fact-report. Every probe result —
// alive, dead, or failed — is reported as a fact to the LCM. The reaper does
// not optimize away the "alive" case, because a session in Detecting (whose
// runtime axis is NOT alive) is included in the running set and needs the
// alive probe to recover; the reaper has no business deciding what counts as
// a no-op. The LCM's ApplyRuntimeObservation diffs against canonical and
// only Upserts on actual change, so steady-state alive is already cheap.
func (r *Reaper) probeOne(ctx context.Context, sess domain.SessionRecord, now time.Time) {
	handle, ok := handleFromRecord(sess)
	if !ok {
		// A session in the running-set without a handle is an anomaly worth
		// surfacing (OnSpawnCompleted should have set both keys). Warn rather
		// than Debug so it doesn't hide behind a noisy log level.
		r.logger.Warn("reaper: session has no runtime handle metadata, skipping",
			"session", sess.ID)
		return
	}
	rt, ok := r.registry.Runtime(handle.RuntimeName)
	if !ok {
		r.logger.Warn("reaper: no runtime registered for session, skipping",
			"session", sess.ID, "runtime", handle.RuntimeName)
		return
	}

	alive, probeErr := rt.IsAlive(ctx, handle)
	facts := ports.RuntimeFacts{ObservedAt: now}
	switch {
	case probeErr != nil:
		// Failed probe must NOT be collapsed to alive — that would let a
		// transient tmux/zellij outage hide a really-dead session, and a
		// transient adapter bug terminate a really-alive one. Report failed
		// and let the LCM's detecting quarantine arbitrate.
		facts.RuntimeState = ports.RuntimeProbeFailed
		facts.ProcessState = ports.ProcessProbeFailed
		r.logger.Debug("reaper: probe error reported as failed fact",
			"session", sess.ID, "runtime", handle.RuntimeName, "err", probeErr)
	case alive:
		facts.RuntimeState = ports.RuntimeProbeAlive
		facts.ProcessState = ports.ProcessProbeAlive
	default:
		facts.RuntimeState = ports.RuntimeProbeDead
		facts.ProcessState = ports.ProcessProbeDead
	}

	if err := r.lcm.ApplyRuntimeObservation(ctx, sess.ID, facts); err != nil {
		r.logger.Error("reaper: ApplyRuntimeObservation failed",
			"session", sess.ID, "err", err)
	}
}

// handleFromRecord reconstructs the RuntimeHandle stored on the session by
// OnSpawnCompleted. Both keys are required; either being empty is the
// "session lacks a probable handle" signal that probeOne uses to skip.
func handleFromRecord(rec domain.SessionRecord) (ports.RuntimeHandle, bool) {
	id := rec.Metadata[lifecycle.MetaRuntimeHandleID]
	name := rec.Metadata[lifecycle.MetaRuntimeName]
	if id == "" || name == "" {
		return ports.RuntimeHandle{}, false
	}
	return ports.RuntimeHandle{ID: id, RuntimeName: name}, true
}
