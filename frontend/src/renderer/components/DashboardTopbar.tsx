import { useNavigate } from "@tanstack/react-router";
import { Bell, Plus, Settings } from "lucide-react";
import { attentionZone } from "../types/workspace";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { useShell } from "../lib/shell-context";
import { cn } from "../lib/utils";

type DashboardTab = "coding" | "reviews";

type DashboardTopbarProps = {
  /** Which top-nav tab reads as active (omit on the PR board, which is neither). */
  activeTab?: DashboardTab;
  /** When set, the project crumb + settings gear scope to one project. */
  projectId?: string;
  projectLabel?: string;
};

// The dashboard header (mc-board .dashboard-app-header): project crumb · Coding/
// Reviews tabs · "N working" breathing pill | bell · settings · New worker.
// Shared verbatim across the board, review, and PR screens so navigating between
// them keeps one stable top strip (agent-orchestrator surfaces them as tabs).
export function DashboardTopbar({ activeTab, projectId, projectLabel = "agent-orchestrator" }: DashboardTopbarProps) {
  const navigate = useNavigate();
  const { openSpawn } = useShell();
  const all = useWorkspaceQuery().data ?? [];
  const sessions = (projectId ? all.filter((w) => w.id === projectId) : all).flatMap((w) => w.sessions);
  const working = sessions.filter((s) => attentionZone(s) === "working").length;

  const tabClass = (tab: DashboardTab) =>
    cn(
      "h-7 rounded-md px-[11px] text-[12.5px] transition-colors",
      activeTab === tab ? "bg-white/[0.07] text-[#f4f5f7]" : "text-[#646a73] hover:text-[#f4f5f7]",
    );

  return (
    <header className="flex h-14 shrink-0 items-center gap-[13px] px-5">
      <span className="text-[14.5px] font-semibold tracking-[-0.01em] text-[#f4f5f7]">{projectLabel}</span>
      <nav className="ml-1.5 flex items-center gap-0.5">
        <button
          className={tabClass("coding")}
          onClick={() => void navigate(projectId ? { to: "/projects/$projectId", params: { projectId } } : { to: "/" })}
          type="button"
        >
          Coding
        </button>
        <button className={tabClass("reviews")} onClick={() => void navigate({ to: "/review" })} type="button">
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
  );
}

// The board subhead (mc-board .dashboard-main__subhead): a 21px bold title with
// a muted one-line subtitle, optionally a trailing count.
export function DashboardSubhead({ title, subtitle, count }: { title: string; subtitle: string; count?: number }) {
  return (
    <div className="flex items-baseline gap-3 px-[18px] pt-[22px]">
      <h1 className="text-[21px] font-bold tracking-[-0.025em] text-[#f4f5f7]">{title}</h1>
      {typeof count === "number" && <span className="font-mono text-[13px] text-[#646a73]">{count}</span>}
      <span className="text-[12.5px] text-[#646a73]">{subtitle}</span>
    </div>
  );
}
