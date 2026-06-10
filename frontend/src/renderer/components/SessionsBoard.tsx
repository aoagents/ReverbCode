import { useNavigate } from "@tanstack/react-router";
import { Bell, Plus, Settings } from "lucide-react";
import {
  type AttentionZone,
  type WorkerDisplayStatus,
  type WorkspaceSession,
  attentionZone,
  workerDisplayStatus,
} from "../types/workspace";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { useShell } from "../lib/shell-context";
import { cn } from "../lib/utils";

type SessionsBoardProps = {
  /** When set, the board shows only this project's sessions. */
  projectId?: string;
};

// The four kanban columns, left→right by flow (work → review → merge), ported
// verbatim from agent-orchestrator (SIMPLE_KANBAN_LEVELS + AttentionZone +
// mc-board.css). "done" is archived, not a column.
type Column = { level: AttentionZone; label: string; glow: string; dot: string; dotGlow: boolean; title: string };
const COLUMNS: Column[] = [
  { level: "working", label: "Working", glow: "color-mix(in srgb, #f59f4c 7%, transparent)", dot: "#f59f4c", dotGlow: true, title: "rgb(231, 180, 131)" },
  { level: "action", label: "Needs you", glow: "color-mix(in srgb, #e8c14a 6%, transparent)", dot: "#e8c14a", dotGlow: true, title: "rgb(227, 207, 141)" },
  { level: "pending", label: "In review", glow: "rgba(255, 255, 255, 0.02)", dot: "#9ba1aa", dotGlow: false, title: "rgb(160, 170, 185)" },
  { level: "merge", label: "Ready to merge", glow: "color-mix(in srgb, #74b98a 7%, transparent)", dot: "#74b98a", dotGlow: true, title: "rgb(150, 200, 168)" },
];

// Card status badge — agent-orchestrator's StatusBadge, reduced to reverbcode's
// glanceable display status.
const BADGE: Record<WorkerDisplayStatus, { label: string; color: string }> = {
  working: { label: "Working", color: "#f59f4c" },
  needs_you: { label: "Needs input", color: "#e8c14a" },
  ci_failed: { label: "CI failed", color: "#ef6b6b" },
  mergeable: { label: "Ready", color: "#74b98a" },
  done: { label: "Done", color: "#646a73" },
};

export function SessionsBoard({ projectId }: SessionsBoardProps) {
  const navigate = useNavigate();
  const workspaceQuery = useWorkspaceQuery();
  const { openSpawn } = useShell();
  const all = workspaceQuery.data ?? [];
  const workspaces = projectId ? all.filter((w) => w.id === projectId) : all;
  const sessions = workspaces.flatMap((w) => w.sessions);
  const projectLabel = projectId ? (workspaces[0]?.name ?? projectId) : "agent-orchestrator";
  const working = sessions.filter((s) => attentionZone(s) === "working").length;

  const byZone = new Map<AttentionZone, WorkspaceSession[]>();
  for (const session of sessions) {
    const zone = attentionZone(session);
    (byZone.get(zone) ?? byZone.set(zone, []).get(zone)!).push(session);
  }
  const done = byZone.get("done") ?? [];

  const openSession = (session: WorkspaceSession) =>
    void navigate({
      to: "/projects/$projectId/sessions/$sessionId",
      params: { projectId: session.workspaceId, sessionId: session.id },
    });

  return (
    <div className="flex h-full min-h-0 flex-col bg-[#0a0b0d] text-[#f4f5f7]">
      {/* Topbar (mc-board .dashboard-app-header): crumb · tabs · working pill | bell · primary */}
      <header className="flex h-14 shrink-0 items-center gap-[13px] px-5">
        <span className="text-[14.5px] font-semibold tracking-[-0.01em] text-[#f4f5f7]">{projectLabel}</span>
        <nav className="ml-1.5 flex items-center gap-0.5">
          <button className="h-7 rounded-md bg-white/[0.07] px-[11px] text-[12.5px] text-[#f4f5f7]" type="button">
            Coding
          </button>
          <button
            className="h-7 rounded-md px-[11px] text-[12.5px] text-[#646a73] transition-colors hover:text-[#f4f5f7]"
            onClick={() => void navigate({ to: "/review" })}
            type="button"
          >
            Reviews
          </button>
        </nav>
        {working > 0 && (
          <span className="inline-flex items-center gap-[7px] rounded-md px-[11px] py-[5px] text-[#9ba1aa] shadow-[inset_0_0_0_1px_rgba(255,255,255,0.06)]">
            <span className="h-[7px] w-[7px] animate-status-pulse rounded-full bg-[#f59f4c]" />
            <span className="text-[11.5px]">{working} working</span>
          </span>
        )}
        <div className="ml-auto flex items-center gap-1.5">
          <button
            aria-label="Notifications"
            className="grid h-[34px] w-[34px] place-items-center rounded-[7px] text-[#9ba1aa] transition-colors hover:bg-white/[0.04] hover:text-[#f4f5f7]"
            type="button"
          >
            <Bell className="h-[15px] w-[15px]" aria-hidden="true" />
          </button>
          {projectId && (
            <button
              aria-label="Project settings"
              className="grid h-[34px] w-[34px] place-items-center rounded-[7px] text-[#9ba1aa] transition-colors hover:bg-white/[0.04] hover:text-[#f4f5f7]"
              onClick={() => void navigate({ to: "/projects/$projectId/settings", params: { projectId } })}
              type="button"
            >
              <Settings className="h-[15px] w-[15px]" aria-hidden="true" />
            </button>
          )}
          <button
            className="inline-flex h-[34px] items-center gap-1.5 rounded-[7px] bg-[#4d8dff] px-[15px] text-[13px] font-semibold text-white transition-colors hover:brightness-110"
            onClick={() => openSpawn(projectId)}
            type="button"
          >
            <Plus className="h-3.5 w-3.5" aria-hidden="true" />
            New worker
          </button>
        </div>
      </header>

      {/* Board subhead (mc-board .dashboard-main__subhead) */}
      <div className="flex items-baseline gap-3 px-[18px] pt-[22px]">
        <h1 className="text-[21px] font-bold tracking-[-0.025em] text-[#f4f5f7]">Board</h1>
        <span className="text-[12.5px] text-[#646a73]">Live agent sessions flowing from work → review → merge.</span>
      </div>

      <div className="min-h-0 flex-1 overflow-hidden p-[18px]">
        {workspaceQuery.isError ? (
          <p className="py-10 text-center text-[12px] text-[#646a73]">Could not load sessions.</p>
        ) : (
          <div className="grid h-full grid-cols-4 gap-2">
            {COLUMNS.map((col) => (
              <ZoneColumn key={col.level} col={col} sessions={byZone.get(col.level) ?? []} onOpen={openSession} />
            ))}
          </div>
        )}
      </div>

      {done.length > 0 && (
        <div className="shrink-0 border-t border-white/[0.06] px-[18px] py-2.5">
          <div className="mb-1.5 flex items-center gap-2">
            <span className="h-[7px] w-[7px] rounded-full bg-[#646a73]" />
            <span className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#9ba1aa]">Done</span>
            <span className="font-mono text-[11px] text-[#646a73]">{done.length}</span>
          </div>
          <div className="flex flex-wrap gap-2">
            {done.map((s) => (
              <button
                key={s.id}
                className="rounded-[7px] border border-white/[0.06] bg-[#15171b] px-2.5 py-1.5 text-left transition-colors hover:border-white/[0.1]"
                onClick={() => openSession(s)}
                type="button"
              >
                <span className="text-[12px] text-[#9ba1aa]">{s.title}</span>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function ZoneColumn({ col, sessions, onOpen }: { col: Column; sessions: WorkspaceSession[]; onOpen: (s: WorkspaceSession) => void }) {
  return (
    <section
      className="flex min-w-0 flex-col overflow-hidden rounded-[13px]"
      style={{ background: `linear-gradient(180deg, ${col.glow}, transparent 130px), rgba(255, 255, 255, 0.018)` }}
    >
      <div className="flex shrink-0 items-center gap-[9px] px-[15px] pb-[11px] pt-[14px]">
        <span
          className="h-[7px] w-[7px] rounded-full"
          style={{ background: col.dot, boxShadow: col.dotGlow ? `0 0 7px color-mix(in srgb, ${col.dot} 60%, transparent)` : undefined }}
        />
        <span className="text-[11px] font-semibold uppercase tracking-[0.08em]" style={{ color: col.title }}>
          {col.label}
        </span>
        <span className="ml-auto font-mono text-[11px] leading-none text-[#646a73]">{sessions.length}</span>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-[11px] pb-3">
        <div className="flex flex-col gap-2.5">
          {sessions.map((session) => (
            <SessionCard key={session.id} session={session} onOpen={() => onOpen(session)} />
          ))}
        </div>
      </div>
    </section>
  );
}

function SessionCard({ session, onOpen }: { session: WorkspaceSession; onOpen: () => void }) {
  const badge = BADGE[workerDisplayStatus(session)];
  const branch = session.branch || `session/${session.id}`;
  return (
    <button
      className="w-full rounded-[7px] border border-white/[0.06] bg-[#15171b] text-left transition-colors hover:border-white/[0.12]"
      onClick={onOpen}
      type="button"
    >
      <div className="flex items-center gap-2 px-[13px] pb-[9px] pt-3">
        <span className="inline-flex items-center gap-1.5 text-[11px] font-medium" style={{ color: badge.color }}>
          <span className="h-[7px] w-[7px] rounded-full" style={{ background: badge.color }} />
          {badge.label}
        </span>
        <span className="ml-auto shrink-0 font-mono text-[10.5px] tracking-[0.04em] text-[#646a73]">{session.id}</span>
      </div>
      <div
        className={cn(
          "px-[13px] pb-2.5 text-[13px] font-medium leading-[1.42] tracking-[-0.01em] text-[#f4f5f7]",
          "line-clamp-2 overflow-hidden",
        )}
      >
        {session.title}
      </div>
      <div className="px-[13px] pb-2.5 font-mono text-[10.5px] text-[#646a73]">{branch}</div>
      <div className="border-t border-white/[0.06] px-[13px] py-2 font-mono text-[10.5px] text-[#646a73]">
        {session.pullRequest ? `PR #${session.pullRequest.number} · ${session.pullRequest.state}` : "no PR yet"}
      </div>
    </button>
  );
}
