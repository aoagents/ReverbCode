import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import {
	type AttentionZone,
	type WorkerDisplayStatus,
	type WorkspaceSession,
	attentionZone,
	workerDisplayStatus,
	workerSessions,
} from "../types/workspace";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { DashboardSubhead } from "./DashboardSubhead";
import { cn } from "../lib/utils";

type SessionsBoardProps = {
	/** When set, the board shows only this project's sessions. */
	projectId?: string;
};

// The four kanban columns, left→right by flow (work → review → merge), ported
// verbatim from agent-orchestrator (SIMPLE_KANBAN_LEVELS + AttentionZone +
// mc-board.css). "done" is archived, not a column.
type Column = {
	level: AttentionZone;
	label: string;
	glow: string;
	dot: string;
	dotGlow: boolean;
	titleClass: string;
};
const COLUMNS: Column[] = [
	{
		level: "working",
		label: "Working",
		glow: "color-mix(in srgb, var(--orange) 7%, transparent)",
		dot: "var(--orange)",
		dotGlow: true,
		titleClass: "text-working",
	},
	{
		level: "action",
		label: "Needs you",
		glow: "color-mix(in srgb, var(--amber) 6%, transparent)",
		dot: "var(--amber)",
		dotGlow: true,
		titleClass: "text-warning",
	},
	{
		level: "pending",
		label: "In review",
		glow: "var(--kanban-pending-glow)",
		dot: "var(--fg-muted)",
		dotGlow: false,
		titleClass: "text-muted-foreground",
	},
	{
		level: "merge",
		label: "Ready to merge",
		glow: "color-mix(in srgb, var(--green) 7%, transparent)",
		dot: "var(--green)",
		dotGlow: true,
		titleClass: "text-success",
	},
];

const BADGE: Record<WorkerDisplayStatus, { label: string; className: string }> = {
	working: { label: "Working", className: "text-working" },
	needs_you: { label: "Needs input", className: "text-warning" },
	ci_failed: { label: "CI failed", className: "text-error" },
	mergeable: { label: "Ready", className: "text-success" },
	done: { label: "Done", className: "text-passive" },
};

export function SessionsBoard({ projectId }: SessionsBoardProps) {
	const navigate = useNavigate();
	const workspaceQuery = useWorkspaceQuery();
	const all = workspaceQuery.data ?? [];
	const workspaces = projectId ? all.filter((w) => w.id === projectId) : all;
	// Project board leads with the project's name; the cross-project board
	// (home) keeps the generic title.
	const boardTitle = (projectId && workspaces[0]?.name) || "Board";
	const sessions = workspaces.flatMap((w) => workerSessions(w.sessions));

	const byZone = new Map<AttentionZone, WorkspaceSession[]>();
	for (const session of sessions) {
		const zone = attentionZone(session);
		(byZone.get(zone) ?? byZone.set(zone, []).get(zone)!).push(session);
	}
	const done = byZone.get("done") ?? [];
	// Collapsed by default, like agent-orchestrator's done-bar: finished and
	// killed sessions cost one quiet line under the board until expanded.
	const [doneExpanded, setDoneExpanded] = useState(false);

	const openSession = (session: WorkspaceSession) =>
		void navigate({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: session.workspaceId, sessionId: session.id },
		});

	return (
		<div className="flex h-full min-h-0 flex-col bg-background text-foreground">
			<DashboardSubhead title="Board" subtitle="Live agent sessions flowing from work → review → merge." />

			<div className="min-h-0 flex-1 overflow-hidden p-[18px]">
				{workspaceQuery.isError ? (
					<p className="py-10 text-center text-[12px] text-passive">Could not load sessions.</p>
				) : (
					<div className="grid h-full grid-cols-4 gap-2">
						{COLUMNS.map((col) => (
							<ZoneColumn key={col.level} col={col} sessions={byZone.get(col.level) ?? []} onOpen={openSession} />
						))}
					</div>
				)}
			</div>

			{done.length > 0 && (
				<div className="shrink-0 border-t border-border px-[18px]">
					{/* agent-orchestrator's done-bar (Dashboard.tsx + globals.css):
					    a full-width chevron + label + count toggle row. min-h matches
					    the sidebar footer (7px pad ×2 + 37px Settings button) so this
					    border-t aligns with the sidebar's footer border. The button is
					    37px (not the 35.5px its text-[13px] implies) because the
					    unlayered `button { font: inherit }` in styles.css outranks
					    Tailwind's layered text utilities, leaving it at 14px/21px. */}
					<button
						aria-expanded={doneExpanded}
						className="group flex min-h-[51px] w-full items-center gap-2 py-2 text-muted-foreground transition-colors hover:text-foreground"
						onClick={() => setDoneExpanded((v) => !v)}
						type="button"
					>
						<svg
							aria-hidden="true"
							className={cn("h-3 w-3 shrink-0 transition-transform duration-150", doneExpanded && "rotate-90")}
							fill="none"
							stroke="currentColor"
							strokeWidth="2"
							viewBox="0 0 24 24"
						>
							<path d="m9 18 6-6-6-6" />
						</svg>
						<span className="font-mono text-[10.5px] font-medium uppercase tracking-[0.05em]">Done / Terminated</span>
						<span className="ml-auto shrink-0 font-mono text-[10px] text-passive">{done.length}</span>
					</button>
					{doneExpanded && (
						<div className="flex flex-wrap gap-2 pb-2.5 pt-1">
							{done.map((s) => (
								<button
									key={s.id}
									className="rounded-[7px] border border-border bg-surface px-2.5 py-1.5 text-left transition-colors hover:border-border-strong"
									onClick={() => openSession(s)}
									type="button"
								>
									<span className="text-[12px] text-muted-foreground">{s.title}</span>
								</button>
							))}
						</div>
					)}
				</div>
			)}
		</div>
	);
}

function ZoneColumn({
	col,
	sessions,
	onOpen,
}: {
	col: Column;
	sessions: WorkspaceSession[];
	onOpen: (s: WorkspaceSession) => void;
}) {
	return (
		<section
			className="flex min-w-0 flex-col overflow-hidden rounded-[13px]"
			style={{ background: `linear-gradient(180deg, ${col.glow}, transparent 130px), var(--kanban-column-bg)` }}
		>
			<div className="flex shrink-0 items-center gap-[9px] px-[15px] pb-[11px] pt-[14px]">
				<span
					className="h-[7px] w-[7px] rounded-full"
					style={{
						background: col.dot,
						boxShadow: col.dotGlow ? `0 0 7px color-mix(in srgb, ${col.dot} 60%, transparent)` : undefined,
					}}
				/>
				<span className={cn("text-[11px] font-semibold uppercase tracking-[0.08em]", col.titleClass)}>{col.label}</span>
				<span className="ml-auto font-mono text-[11px] leading-none text-passive">{sessions.length}</span>
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
			className="w-full rounded-[7px] border border-border bg-surface text-left transition-colors hover:border-border-strong"
			onClick={onOpen}
			type="button"
		>
			<div className="flex items-center gap-2 px-[13px] pb-[9px] pt-3">
				<span className={cn("inline-flex items-center gap-1.5 text-[11px] font-medium", badge.className)}>
					<span className={cn("h-[7px] w-[7px] rounded-full bg-current")} />
					{badge.label}
				</span>
				<span className="ml-auto shrink-0 font-mono text-[10.5px] tracking-[0.04em] text-passive">{session.id}</span>
			</div>
			<div
				className={cn(
					"px-[13px] pb-2.5 text-[13px] font-medium leading-[1.42] tracking-[-0.01em] text-foreground",
					"line-clamp-2 overflow-hidden",
				)}
			>
				{session.title}
			</div>
			<div className="px-[13px] pb-2.5 font-mono text-[10.5px] text-passive">{branch}</div>
			<div className="border-t border-border px-[13px] py-2 font-mono text-[10.5px] text-passive">
				{session.pullRequest ? `PR #${session.pullRequest.number} · ${session.pullRequest.state}` : "no PR yet"}
			</div>
		</button>
	);
}
