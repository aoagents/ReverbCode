import { ArrowUp, Check, Columns2 } from "lucide-react";
import type { Theme, WorkbenchView } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { TerminalPane } from "./TerminalPane";

type CenterPaneProps = {
  view: WorkbenchView;
  session?: WorkspaceSession;
  theme: Theme;
  fleet: { agents: number; needYou: number };
};

export function CenterPane({ view, session, theme, fleet }: CenterPaneProps) {
  const isOrchestrator = view === "orchestrator";
  const agentLabel = session?.provider ?? "claude-code";

  return (
    <div className="flex min-w-0 flex-1 flex-col bg-background">
      <div className="flex h-[38px] shrink-0 items-center border-b border-border px-2.5">
        <div className="-mb-px flex h-[38px] items-center gap-2 border-b-2 border-accent px-3 text-[13px] text-foreground">
          <span className="h-[7px] w-[7px] rounded-full bg-success shadow-[0_0_0_3px_rgb(108_177_108_/_0.24)]" />
          {isOrchestrator ? (
            <>
              orchestrator <span className="font-mono text-[11px] text-passive">{agentLabel}</span>
            </>
          ) : (
            <>
              {agentLabel} <span className="font-mono text-[11px] text-passive">(1)</span>
            </>
          )}
        </div>
        {!isOrchestrator && (
          <button
            aria-label="Split terminal"
            className="ml-auto grid h-7 w-7 place-items-center rounded-md text-passive transition-colors hover:bg-raised hover:text-muted-foreground"
            type="button"
          >
            <Columns2 className="h-[15px] w-[15px]" aria-hidden="true" />
          </button>
        )}
      </div>

      <div className="min-h-0 flex-1">
        <TerminalPane session={session} theme={theme} />
      </div>

      <Composer agentLabel={agentLabel} isOrchestrator={isOrchestrator} session={session} fleet={fleet} />
    </div>
  );
}

function Composer({
  agentLabel,
  isOrchestrator,
  session,
  fleet,
}: {
  agentLabel: string;
  isOrchestrator: boolean;
  session?: WorkspaceSession;
  fleet: { agents: number; needYou: number };
}) {
  const worktree = session ? `~/.rc/wt/${session.workspaceName}/${session.title}` : "";

  return (
    <div className="shrink-0 border-t border-border bg-surface px-3 py-2.5">
      <div className="flex flex-col gap-2 rounded-lg border border-border bg-background px-2.5 py-2.5">
        <p className="text-[13px] text-passive">
          {isOrchestrator
            ? "Tell the orchestrator what to build… @ to add files, / for commands"
            : `Message ${agentLabel}… @ to add files, / for commands`}
        </p>
        <div className="flex items-center gap-2 text-[11px] text-passive">
          <span className="inline-flex items-center gap-1.5 rounded-md border border-border px-2 py-0.5 text-muted-foreground">
            {isOrchestrator ? (
              "Plan & delegate"
            ) : (
              <>
                <Check className="h-2.5 w-2.5" aria-hidden="true" />
                Accept edits
              </>
            )}
          </span>
          <span className="rounded-md border border-border px-2 py-0.5 text-muted-foreground">Claude Sonnet 4.6</span>
          <span className="ml-auto truncate font-mono text-[10.5px]">
            {isOrchestrator ? `${fleet.agents} workers · ${fleet.needYou} need you` : worktree}
          </span>
          <button
            aria-label="Send"
            className="grid h-6 w-6 shrink-0 place-items-center rounded-md bg-accent text-accent-foreground transition-opacity hover:opacity-90"
            type="button"
          >
            <ArrowUp className="h-3 w-3" aria-hidden="true" />
          </button>
        </div>
      </div>
    </div>
  );
}
