import type {
  AgentHarness,
  ColumnKey,
  Session,
  SessionKind,
} from "./api";

// Dropping a card on a column sets it to that column's representative status.
// Status is derived server-side from real facts, so this drives a LOCAL override
// (useful for demo and triage); it does not yet persist to the daemon.
export const COLUMN_DEFAULT_STATUS: Record<ColumnKey, Session["status"]> = {
  working: "working",
  needs: "needs_input",
  review: "review_pending",
  merge: "mergeable",
  done: "terminated",
};

// Sample cards for previewing a populated board (the live backend can't keep
// agents alive yet, so real sessions all land in Done/Terminated). Toggled by
// the "Demo" button — clearly not real data.
export const DEMO_SESSIONS: Session[] = (
  [
    ["ao-204", "Brainstorm the design language & component library for the dashboard", "working", "claude-code", "worker"],
    ["int-7", "Wire internal API behind the ALB with Tailscale-only ingress", "draft", "aider", "worker"],
    ["int-6", "Add API-key auth to the integrator service", "needs_input", "codex", "worker"],
    ["int-8", "Sanitize tool output in the Cortex security pipeline", "ci_failed", "claude-code", "worker"],
    ["ao-201", "Produce a high-quality HTML architecture design doc", "review_pending", "claude-code", "worker"],
    ["mer-43", "Auth RCA improvements from the incident review", "changes_requested", "opencode", "worker"],
    ["ao-203", "Build an end-to-end onboarding test for the published npm package", "mergeable", "claude-code", "worker"],
    ["ao-202", "Refactor the storage layer to use sqlc generated queries", "merged", "codex", "worker"],
  ] as const
).map(([id, title, status, harness, kind]) => ({
  id,
  projectId: "demo",
  kind: kind as SessionKind,
  harness: harness as AgentHarness,
  displayName: title,
  activity: { state: "active", lastActivityAt: new Date().toISOString() },
  isTerminated: status === "merged",
  status: status as Session["status"],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
}));
