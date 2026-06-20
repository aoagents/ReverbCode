import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import {
	type SessionStatus,
	type WorkspaceSession,
	STATUS_META,
	openPRs,
	statusOrder,
	workerSessions,
} from "../types/workspace";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { DashboardSubhead } from "./DashboardSubhead";
import { OrchestratorIcon } from "./icons";
import { NewTaskDialog } from "./NewTaskDialog";
import { spawnOrchestrator } from "../lib/spawn-orchestrator";
import { cn } from "../lib/utils";

type SessionsBoardProps = {
	/** When set, the board shows only this project's sessions. */
	projectId?: string;
};

// The five kanban columns, left→right by human-action urgency, one per display
// state ({@link statusOrder}). Each column reads its label and tone from the
// shared STATUS_META so the board, pill, sidebar dot, and card badge never
// disagree. Terminated sessions are archived in the done bar, not a column.
type Column = { status: SessionStatus; label: string; tone: string };
const COLUMNS: Column[] = statusOrder.map((status) => ({
	status,
	label: STATUS_META[status].label,
	tone: STATUS_META[status].tone,
}));

export function SessionsBoard({ projectId }: SessionsBoardProps) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const workspaceQuery = useWorkspaceQuery();
	const all = workspaceQuery.data ?? [];
	const workspaces = projectId ? all.filter((w) => w.id === projectId) : all;
	const sessions = workspaces.flatMap((w) => workerSessions(w.sessions));
	const orchestrator = projectId
		? workspaces[0]?.sessions.find((session) => session.kind === "orchestrator" && !session.isTerminated)
		: undefined;
	const [isNewTaskOpen, setIsNewTaskOpen] = useState(false);
	const [isSpawning, setIsSpawning] = useState(false);

	// Live sessions group into the five status columns; terminated ones archive
	// into the done bar regardless of their (idle) display status.
	const byStatus = new Map<SessionStatus, WorkspaceSession[]>();
	const done: WorkspaceSession[] = [];
	for (const session of sessions) {
		if (session.isTerminated) {
			done.push(session);
			continue;
		}
		(byStatus.get(session.status) ?? byStatus.set(session.status, []).get(session.status)!).push(session);
	}
	// Collapsed by default, like agent-orchestrator's done-bar: finished and
	// killed sessions cost one quiet line under the board until expanded.
	const [doneExpanded, setDoneExpanded] = useState(false);

	const openSession = (session: WorkspaceSession) =>
		void navigate({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: session.workspaceId, sessionId: session.id },
		});

	const openOrchestrator = async () => {
		if (!projectId) return;
		if (orchestrator) {
			void navigate({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId, sessionId: orchestrator.id },
			});
			return;
		}
		setIsSpawning(true);
		try {
			const sessionId = await spawnOrchestrator(projectId);
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			void navigate({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId, sessionId },
			});
		} finally {
			setIsSpawning(false);
		}
	};

	const handleTaskCreated = async (sessionId: string) => {
		if (!projectId) return;
		await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		void navigate({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId, sessionId },
		});
	};

	const actions = projectId ? (
		<>
			<button
				aria-label="New task"
				className="dashboard-app-header__accent-btn"
				onClick={() => setIsNewTaskOpen(true)}
				type="button"
			>
				<Plus className="h-3.5 w-3.5" aria-hidden="true" />
				New task
			</button>
			<button
				aria-label={orchestrator ? "Orchestrator" : "Spawn Orchestrator"}
				className="dashboard-app-header__primary-btn"
				disabled={isSpawning}
				onClick={() => void openOrchestrator()}
				type="button"
			>
				<OrchestratorIcon className="h-3.5 w-3.5" aria-hidden="true" />
				{isSpawning ? "Spawning..." : orchestrator ? "Orchestrator" : "Spawn Orchestrator"}
			</button>
		</>
	) : undefined;

	return (
		<div className="flex h-full min-h-0 flex-col bg-background text-foreground">
			<DashboardSubhead
				title="Board"
				subtitle="Live agent sessions flowing from work → review → merge."
				actions={actions}
			/>

			<div className="min-h-0 flex-1 overflow-hidden p-[18px]">
				{workspaceQuery.isError ? (
					<p className="py-10 text-center text-[12px] text-passive">Could not load sessions.</p>
				) : (
					<div className="grid h-full grid-cols-5 gap-2">
						{COLUMNS.map((col) => (
							<ZoneColumn key={col.status} col={col} sessions={byStatus.get(col.status) ?? []} onOpen={openSession} />
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
			<NewTaskDialog
				open={isNewTaskOpen}
				projectId={projectId}
				onCreated={(sessionId) => void handleTaskCreated(sessionId)}
				onOpenChange={setIsNewTaskOpen}
			/>
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
	const glow = col.status === "idle" ? undefined : `0 0 7px color-mix(in srgb, ${col.tone} 60%, transparent)`;
	return (
		<section
			className="flex min-w-0 flex-col overflow-hidden rounded-[13px]"
			style={{
				background: `linear-gradient(180deg, color-mix(in srgb, ${col.tone} 7%, transparent), transparent 130px), var(--kanban-column-bg)`,
			}}
		>
			<div className="flex shrink-0 items-center gap-[9px] px-[15px] pb-[11px] pt-[14px]">
				<span className="h-[7px] w-[7px] rounded-full" style={{ background: col.tone, boxShadow: glow }} />
				<span className="text-[11px] font-semibold uppercase tracking-[0.08em]" style={{ color: col.tone }}>
					{col.label}
				</span>
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

// One-line PR summary for the card footer. A session can own several PRs, so
// collapse to a count once past one; detail lives in the inspector stack.
function prSummary(session: WorkspaceSession): string {
	const total = session.prs.length;
	if (total === 0) return "no PR yet";
	if (total === 1) {
		const pr = session.prs[0];
		return `PR #${pr.number} · ${pr.state}`;
	}
	const open = openPRs(session).length;
	return open > 0 ? `${total} PRs · ${open} open` : `${total} PRs`;
}

function SessionCard({ session, onOpen }: { session: WorkspaceSession; onOpen: () => void }) {
	const badge = sessionBadge(session);
	const branch = session.branch || "";
	const showBranch = branch !== "" && !sameLabel(branch, session.title) && !sameLabel(branch, session.id);
	return (
		<button
			className="w-full rounded-[7px] border border-border bg-surface text-left transition-colors hover:border-border-strong"
			onClick={onOpen}
			type="button"
		>
			<div className="flex items-center gap-2 px-[13px] pb-[9px] pt-3">
				<span className="inline-flex items-center gap-1.5 text-[11px] font-medium" style={{ color: badge.tone }}>
					<span className="h-[7px] w-[7px] rounded-full bg-current" />
					{badge.label}
				</span>
				<span className="ml-auto shrink-0 font-mono text-[10.5px] tracking-[0.04em] text-passive">
					{agentLabel(session.provider)}
				</span>
			</div>
			<div
				className={cn(
					"px-[13px] text-[13px] font-medium leading-[1.42] tracking-[-0.01em] text-foreground",
					showBranch ? "pb-2" : "pb-3",
					"line-clamp-2 overflow-hidden",
				)}
			>
				{session.title}
			</div>
			{showBranch && <div className="px-[13px] pb-2.5 font-mono text-[10.5px] text-passive">{branch}</div>}
			<div className="border-t border-border px-[13px] py-2 font-mono text-[10.5px] text-passive">
				{prSummary(session)}
			</div>
		</button>
	);
}

function sameLabel(a: string, b: string): boolean {
	const normalize = (value: string) =>
		value
			.toLowerCase()
			.replace(/^(feat|fix|chore|refactor|session)\//, "")
			.replace(/[^a-z0-9]+/g, "");
	return normalize(a) === normalize(b);
}

function agentLabel(provider: WorkspaceSession["provider"]): string {
	switch (provider) {
		case "claude-code":
			return "Claude";
		case "opencode":
			return "OpenCode";
		default:
			return provider;
	}
}

function sessionBadge(session: WorkspaceSession): { label: string; tone: string } {
	const meta = STATUS_META[session.status];
	return { label: meta.label, tone: meta.tone };
}
