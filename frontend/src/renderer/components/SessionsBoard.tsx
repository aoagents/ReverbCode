import { useNavigate } from "@tanstack/react-router";
import { GitPullRequest, LoaderCircle, Plus } from "lucide-react";
import {
  type AttentionZone,
  type WorkerDisplayStatus,
  type WorkspaceSession,
  attentionZone,
  attentionZoneLabel,
  attentionZoneOrder,
  workerDisplayStatus,
  workerStatusLabel,
  workerStatusPulses,
} from "../types/workspace";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { useShell } from "../lib/shell-context";
import { Card, CardContent } from "./ui/card";
import { cn } from "../lib/utils";

type SessionsBoardProps = {
  /** When set, the board shows only this project's sessions. */
  projectId?: string;
};

const dotTone: Record<WorkerDisplayStatus, string> = {
  working: "bg-accent",
  needs_you: "bg-warning",
  mergeable: "bg-success",
  ci_failed: "bg-error",
  done: "bg-passive",
};

// Column accent — leftmost (merge) reads "go", action reads "attention".
const zoneTone: Record<AttentionZone, string> = {
  merge: "bg-success",
  action: "bg-warning",
  pending: "bg-passive",
  working: "bg-accent",
  done: "bg-passive",
};

// The board is the app home, mirroring agent-orchestrator's attention-zone
// kanban: sessions bucket into urgency-ordered columns (merge → needs-you →
// pending → working → done) so the highest-ROI work sits leftmost. Clicking a
// card navigates into the session's detail/terminal route.
export function SessionsBoard({ projectId }: SessionsBoardProps) {
  const navigate = useNavigate();
  const workspaceQuery = useWorkspaceQuery();
  const { openSpawn } = useShell();
  const all = workspaceQuery.data ?? [];
  const workspaces = projectId ? all.filter((w) => w.id === projectId) : all;
  const sessions = workspaces.flatMap((w) => w.sessions);
  const heading = projectId ? (workspaces[0]?.name ?? projectId) : "Orchestrator";

  const byZone = new Map<AttentionZone, WorkspaceSession[]>();
  for (const session of sessions) {
    const zone = attentionZone(session);
    (byZone.get(zone) ?? byZone.set(zone, []).get(zone)!).push(session);
  }
  const columns = attentionZoneOrder.filter((zone) => (byZone.get(zone)?.length ?? 0) > 0);

  const openSession = (session: WorkspaceSession) =>
    void navigate({
      to: "/projects/$projectId/sessions/$sessionId",
      params: { projectId: session.workspaceId, sessionId: session.id },
    });

  return (
    <div className="flex h-full min-h-0 flex-col bg-background text-foreground">
      <header className="flex h-11 shrink-0 items-center gap-2.5 border-b border-border px-4">
        <span className="text-[13.5px] font-semibold text-foreground">{heading}</span>
        <span className="font-mono text-[11px] text-passive">{sessions.length} sessions</span>
        <button
          aria-label="New worker"
          className="ml-auto inline-flex h-6 items-center gap-1.5 rounded-md border border-border px-2.5 text-[11.5px] text-muted-foreground transition-colors hover:border-accent hover:text-accent"
          onClick={() => openSpawn(projectId)}
          type="button"
        >
          <Plus className="h-3 w-3" aria-hidden="true" />
          New worker
        </button>
      </header>

      {workspaceQuery.isError ? (
        <p className="py-10 text-center text-[12px] text-passive">Could not load sessions.</p>
      ) : sessions.length === 0 ? (
        <p className="py-10 text-center text-[12px] text-passive">
          No workers yet. Use <span className="text-foreground">New worker</span> to spawn one.
        </p>
      ) : (
        <div className="flex min-h-0 flex-1 gap-3 overflow-x-auto p-4">
          {columns.map((zone) => (
            <ZoneColumn key={zone} zone={zone} sessions={byZone.get(zone) ?? []} onOpen={openSession} />
          ))}
        </div>
      )}
    </div>
  );
}

function ZoneColumn({
  zone,
  sessions,
  onOpen,
}: {
  zone: AttentionZone;
  sessions: WorkspaceSession[];
  onOpen: (session: WorkspaceSession) => void;
}) {
  return (
    <section className="flex w-[300px] shrink-0 flex-col">
      <div className="mb-2 flex items-center gap-2 px-1">
        <span className={cn("h-[7px] w-[7px] rounded-full", zoneTone[zone])} />
        <span className="text-[12px] font-medium text-foreground">{attentionZoneLabel[zone]}</span>
        <span className="font-mono text-[11px] text-passive">{sessions.length}</span>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto pr-0.5">
        {sessions.map((session) => (
          <SessionCard key={session.id} session={session} onOpen={() => onOpen(session)} />
        ))}
      </div>
    </section>
  );
}

function SessionCard({ session, onOpen }: { session: WorkspaceSession; onOpen: () => void }) {
  const status = workerDisplayStatus(session);
  return (
    <Card
      className="cursor-pointer gap-0 py-0 transition-colors hover:border-accent"
      onClick={onOpen}
      role="button"
      tabIndex={0}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onOpen();
        }
      }}
    >
      <CardContent className="flex flex-col gap-1.5 p-3">
        <div className="flex items-center gap-2">
          {status === "working" ? (
            <LoaderCircle className="h-3.5 w-3.5 shrink-0 animate-spin text-accent" aria-hidden="true" />
          ) : (
            <span
              className={cn(
                "h-[7px] w-[7px] shrink-0 rounded-full",
                dotTone[status],
                workerStatusPulses(status) && "animate-status-pulse",
              )}
              title={workerStatusLabel[status]}
            />
          )}
          <span className="min-w-0 flex-1 truncate text-[13px] text-foreground">{session.title}</span>
          {session.pullRequest && (
            <span className="inline-flex shrink-0 items-center gap-1 font-mono text-[10px] text-passive">
              <GitPullRequest className="h-3 w-3" aria-hidden="true" />#{session.pullRequest.number}
            </span>
          )}
        </div>
        <div className="truncate font-mono text-[10px] text-passive">
          {[session.workspaceName, session.branch].filter(Boolean).join(" · ")}
        </div>
      </CardContent>
    </Card>
  );
}
