import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, CircleAlert, CircleDot, GitMerge, GitPullRequest, MessageSquare } from "lucide-react";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import type { WorkspaceSession } from "../types/workspace";
import { Badge } from "./ui/badge";
import { cn } from "../lib/utils";

type PRFacts = components["schemas"]["SessionPRFacts"];

const stateTone: Record<PRFacts["state"], string> = {
  open: "border-success/40 bg-success/10 text-success",
  draft: "border-border bg-raised text-muted-foreground",
  merged: "border-accent/40 bg-accent-weak text-accent",
  closed: "border-error/40 bg-error/10 text-error",
};

// Read a free-text fact string ("passing"/"failing"/"approved"/…) into a tone.
function factTone(value: string): "ok" | "warn" | "bad" | "muted" {
  const v = value.toLowerCase();
  if (/pass|approv|mergeable|clean|success|ready/.test(v)) return "ok";
  if (/fail|conflict|error|blocked|rejected|changes/.test(v)) return "bad";
  if (/pending|review|waiting|running|unknown|none/.test(v)) return "warn";
  return "muted";
}

const toneClass: Record<ReturnType<typeof factTone>, string> = {
  ok: "text-success",
  warn: "text-warning",
  bad: "text-error",
  muted: "text-passive",
};

// PR inspector for the session rail — ported from agent-orchestrator's
// SessionInspector, on reverbcode's SessionPRFacts (GET /sessions/{id}/pr).
// Renders nothing when the session has no PR; the git rail's "Create PR"
// covers that case.
export function SessionInspector({ session }: { session?: WorkspaceSession }) {
  const sessionId = session?.id;
  const hasPr = Boolean(session?.pullRequest);

  const query = useQuery({
    queryKey: ["session-pr", sessionId],
    enabled: Boolean(sessionId) && hasPr,
    queryFn: async () => {
      const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/pr", {
        params: { path: { sessionId: sessionId! } },
      });
      if (error) return [] as PRFacts[];
      return data?.prs ?? [];
    },
  });

  if (!hasPr) return null;
  const pr = query.data?.[0];

  return (
    <section className="border-b border-border px-3 py-2.5">
      <div className="mb-1.5 flex items-center gap-2">
        <GitPullRequest className="h-3.5 w-3.5 shrink-0 text-passive" aria-hidden="true" />
        <span className="text-[12px] font-medium text-foreground">
          PR {pr ? `#${pr.number}` : session?.pullRequest ? `#${session.pullRequest.number}` : ""}
        </span>
        {pr && (
          <Badge variant="outline" className={cn("ml-auto h-5 px-1.5 text-[10px] font-medium", stateTone[pr.state])}>
            {pr.state}
          </Badge>
        )}
      </div>
      {query.isLoading ? (
        <p className="font-mono text-[11px] text-passive">loading PR facts…</p>
      ) : !pr ? (
        <p className="font-mono text-[11px] text-passive">no enriched PR facts yet</p>
      ) : (
        <div className="flex flex-col gap-1">
          <FactRow icon={CircleDot} label="CI" value={pr.ci} />
          <FactRow icon={GitMerge} label="Mergeability" value={pr.mergeability} />
          <FactRow icon={CheckCircle2} label="Review" value={pr.review} />
          {pr.reviewComments && (
            <div className="flex items-center gap-1.5 text-[11px] text-warning">
              <MessageSquare className="h-3 w-3 shrink-0" aria-hidden="true" />
              unresolved review comments
            </div>
          )}
        </div>
      )}
    </section>
  );
}

function FactRow({ icon: Icon, label, value }: { icon: typeof CircleAlert; label: string; value: string }) {
  return (
    <div className="flex items-center gap-1.5 text-[11px]">
      <Icon className="h-3 w-3 shrink-0 text-passive" aria-hidden="true" />
      <span className="text-passive">{label}</span>
      <span className={cn("ml-auto truncate font-mono", toneClass[factTone(value)])}>{value || "—"}</span>
    </div>
  );
}
