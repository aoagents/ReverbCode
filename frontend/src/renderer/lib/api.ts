// Typed client over the `window.ao` bridge. Every method maps to one daemon
// REST route; the bridge proxies through the Electron main process, so there is
// no fetch/CORS here. Types mirror backend/internal/httpd/controllers/dto.go.

export interface AoResponse<T = unknown> {
  ok: boolean;
  status: number;
  data: T;
  error?: string;
}

declare global {
  interface Window {
    ao: {
      request<T = unknown>(req: {
        method: string;
        path: string;
        query?: Record<string, string | number | boolean | undefined>;
        body?: unknown;
      }): Promise<AoResponse<T>>;
    };
  }
}

// ── Wire types (subset of the daemon DTOs we render) ──────────────────────

export interface ProjectSummary {
  id: string;
  name: string;
  sessionPrefix: string;
  resolveError?: string;
}

export interface Project {
  id: string;
  name: string;
  path: string;
  repo: string;
  defaultBranch: string;
  agent?: string;
}

export type SessionStatus =
  | "working"
  | "pr_open"
  | "draft"
  | "ci_failed"
  | "review_pending"
  | "changes_requested"
  | "approved"
  | "mergeable"
  | "merged"
  | "needs_input"
  | "idle"
  | "terminated";

export type SessionKind = "worker" | "orchestrator";
export type AgentHarness = "claude-code" | "codex" | "aider" | "opencode";

export interface Session {
  id: string;
  projectId: string;
  issueId?: string;
  kind: SessionKind;
  harness?: AgentHarness;
  displayName?: string;
  activity: { state: string; lastActivityAt: string };
  isTerminated: boolean;
  status: SessionStatus;
  createdAt: string;
  updatedAt: string;
}

export interface ApiError {
  code: string;
  message: string;
}

// Board model mirrors the product dashboard: four active columns plus a
// collapsed Done/Terminated tray. Each status maps to a column and carries a
// display label + colour tone for its card chip and sidebar dot.
export type ColumnKey = "working" | "needs" | "review" | "merge" | "done";
export type Tone = "orange" | "amber" | "blue" | "green" | "purple" | "red" | "dim";

export interface StatusMeta {
  label: string;
  tone: Tone;
  column: ColumnKey;
}

export const STATUS_META: Record<SessionStatus, StatusMeta> = {
  working: { label: "Working", tone: "orange", column: "working" },
  idle: { label: "Idle", tone: "dim", column: "working" },
  draft: { label: "Draft", tone: "dim", column: "working" },
  needs_input: { label: "Needs input", tone: "amber", column: "needs" },
  changes_requested: { label: "Changes req.", tone: "red", column: "needs" },
  ci_failed: { label: "CI failed", tone: "red", column: "needs" },
  pr_open: { label: "PR open", tone: "blue", column: "review" },
  review_pending: { label: "Review pending", tone: "blue", column: "review" },
  approved: { label: "Approved", tone: "green", column: "merge" },
  mergeable: { label: "Mergeable", tone: "green", column: "merge" },
  merged: { label: "Merged", tone: "purple", column: "done" },
  terminated: { label: "Terminated", tone: "dim", column: "done" },
};

export function statusMeta(status: SessionStatus): StatusMeta {
  return STATUS_META[status] ?? { label: status, tone: "dim", column: "working" };
}

export interface BoardColumnDef {
  key: ColumnKey;
  title: string;
  tone: Tone;
}

export const BOARD_COLUMNS: BoardColumnDef[] = [
  { key: "working", title: "Working", tone: "orange" },
  { key: "needs", title: "Needs You", tone: "amber" },
  { key: "review", title: "In Review", tone: "blue" },
  { key: "merge", title: "Ready to Merge", tone: "green" },
];

// ── Helpers ───────────────────────────────────────────────────────────────

/** Pull a human-readable message out of the daemon's error envelope. */
function errorMessage(res: AoResponse): string {
  if (res.error) return res.error;
  const data = res.data as { error?: ApiError } | null;
  if (data && data.error) return `${data.error.message} (${data.error.code})`;
  return `request failed (${res.status})`;
}

async function unwrap<T>(p: Promise<AoResponse<T>>): Promise<T> {
  const res = await p;
  if (!res.ok) throw new Error(errorMessage(res));
  return res.data;
}

// ── Health ──────────────────────────────────────────────────────────────

export interface Health {
  status: string;
  service: string;
  pid: number;
}

export async function getHealth(): Promise<Health | null> {
  const res = await window.ao.request<Health>({ method: "GET", path: "/healthz" });
  return res.ok ? res.data : null;
}

// ── Projects ──────────────────────────────────────────────────────────────

export async function listProjects(): Promise<ProjectSummary[]> {
  const data = await unwrap(
    window.ao.request<{ projects: ProjectSummary[] }>({
      method: "GET",
      path: "/api/v1/projects",
    }),
  );
  return data.projects ?? [];
}

export async function addProject(input: {
  path: string;
  projectId?: string;
  name?: string;
}): Promise<Project> {
  const data = await unwrap(
    window.ao.request<{ project: Project }>({
      method: "POST",
      path: "/api/v1/projects",
      body: input,
    }),
  );
  return data.project;
}

export async function removeProject(id: string): Promise<void> {
  await unwrap(
    window.ao.request({
      method: "DELETE",
      path: `/api/v1/projects/${encodeURIComponent(id)}`,
    }),
  );
}

// ── Sessions ──────────────────────────────────────────────────────────────

export async function listSessions(projectId?: string): Promise<Session[]> {
  const data = await unwrap(
    window.ao.request<{ sessions: Session[] }>({
      method: "GET",
      path: "/api/v1/sessions",
      query: projectId ? { project: projectId } : undefined,
    }),
  );
  return data.sessions ?? [];
}

export async function spawnSession(input: {
  projectId: string;
  kind?: SessionKind;
  harness?: AgentHarness;
  branch?: string;
  prompt?: string;
}): Promise<Session> {
  const data = await unwrap(
    window.ao.request<{ session: Session }>({
      method: "POST",
      path: "/api/v1/sessions",
      body: input,
    }),
  );
  return data.session;
}

export async function getSession(id: string): Promise<Session> {
  const data = await unwrap(
    window.ao.request<{ session: Session }>({
      method: "GET",
      path: `/api/v1/sessions/${encodeURIComponent(id)}`,
    }),
  );
  return data.session;
}

export async function sendSessionMessage(
  id: string,
  message: string,
): Promise<void> {
  await unwrap(
    window.ao.request({
      method: "POST",
      path: `/api/v1/sessions/${encodeURIComponent(id)}/send`,
      body: { message },
    }),
  );
}

export async function restoreSession(id: string): Promise<Session> {
  const data = await unwrap(
    window.ao.request<{ session: Session }>({
      method: "POST",
      path: `/api/v1/sessions/${encodeURIComponent(id)}/restore`,
    }),
  );
  return data.session;
}

export async function renameSession(
  id: string,
  displayName: string,
): Promise<void> {
  await unwrap(
    window.ao.request({
      method: "PATCH",
      path: `/api/v1/sessions/${encodeURIComponent(id)}`,
      body: { displayName },
    }),
  );
}

export async function killSession(id: string): Promise<void> {
  await unwrap(
    window.ao.request({
      method: "POST",
      path: `/api/v1/sessions/${encodeURIComponent(id)}/kill`,
    }),
  );
}

export async function cleanupSessions(projectId?: string): Promise<string[]> {
  const data = await unwrap(
    window.ao.request<{ cleaned: string[] }>({
      method: "POST",
      path: "/api/v1/sessions/cleanup",
      query: projectId ? { project: projectId } : undefined,
    }),
  );
  return data.cleaned ?? [];
}
