import {
	GitBranch,
	GitCommitHorizontal,
	GitPullRequest,
	LoaderCircle,
	Plus,
	Square,
	SquareCheck,
	Trash2,
} from "lucide-react";
import { useEffect, useState } from "react";
import type { WorkbenchView } from "../stores/ui-store";
import { useSessionGit } from "../hooks/useSessionGit";
import {
	type WorkerDisplayStatus,
	type WorkspaceSession,
	type WorkspaceSummary,
	workerDisplayStatus,
	workerStatusLabel,
	workerStatusPulses,
} from "../types/workspace";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { cn } from "../lib/utils";

// Session status is a single glyph, no text: spinner while working, a PR icon
// when there's a PR, otherwise a colored dot. See DESIGN.md.
const dotTone: Record<WorkerDisplayStatus, string> = {
	working: "bg-accent",
	needs_you: "bg-warning",
	mergeable: "bg-success",
	ci_failed: "bg-error",
	done: "bg-passive",
};

const prTone: Record<WorkerDisplayStatus, string> = {
	working: "text-accent",
	needs_you: "text-warning",
	mergeable: "text-success",
	ci_failed: "text-error",
	done: "text-muted-foreground",
};

function StatusGlyph({ worker }: { worker: WorkspaceSession }) {
	const status = workerDisplayStatus(worker);
	let glyph: React.ReactNode;
	if (status === "working") {
		glyph = <LoaderCircle className="h-3.5 w-3.5 animate-spin text-accent" aria-hidden="true" />;
	} else if (worker.pullRequest) {
		glyph = <GitPullRequest className={cn("h-3.5 w-3.5", prTone[status])} aria-hidden="true" />;
	} else {
		glyph = (
			<span
				className={cn(
					"h-[7px] w-[7px] rounded-full",
					dotTone[status],
					workerStatusPulses(status) && "animate-status-pulse",
				)}
			/>
		);
	}
	return (
		<span className="grid h-3.5 w-3.5 shrink-0 place-items-center" title={workerStatusLabel[status]}>
			{glyph}
		</span>
	);
}

type SideRailProps = {
	view: WorkbenchView;
	session?: WorkspaceSession;
	workspaces: WorkspaceSummary[];
	onSelectSession: (sessionId: string, workspaceId: string) => void;
};

export function SideRail({ view, session, workspaces, onSelectSession }: SideRailProps) {
	return (
		<aside className="flex h-full w-[316px] shrink-0 flex-col border-l border-border bg-background">
			{view === "orchestrator" ? (
				<WorkersList workspaces={workspaces} onSelectSession={onSelectSession} />
			) : (
				<GitRail session={session} />
			)}
		</aside>
	);
}

function WorkersList({
	workspaces,
	onSelectSession,
}: {
	workspaces: WorkspaceSummary[];
	onSelectSession: (sessionId: string, workspaceId: string) => void;
}) {
	const workers = workspaces.flatMap((workspace) => workspace.sessions);
	return (
		<>
			<SideHead title="Workers" count={workers.length} />
			<div className="min-h-0 flex-1 overflow-y-auto">
				{workers.length === 0 ? (
					<p className="px-3 py-6 text-center text-[12px] text-passive">No workers yet.</p>
				) : (
					workers.map((worker) => {
						const pr = worker.pullRequest;
						const subtitle = [worker.workspaceName, pr ? `PR #${pr.number}` : worker.branch]
							.filter(Boolean)
							.join(" · ");
						return (
							<button
								className="flex h-10 w-full items-center gap-2.5 border-b border-border/50 px-3 text-left transition-colors hover:bg-surface"
								key={worker.id}
								onClick={() => onSelectSession(worker.id, worker.workspaceId)}
								type="button"
							>
								<StatusGlyph worker={worker} />
								<span className="min-w-0 flex-1">
									<span className="block truncate text-[13px] text-foreground">{worker.title}</span>
									<span className="block truncate font-mono text-[10px] text-passive">{subtitle}</span>
								</span>
							</button>
						);
					})
				)}
			</div>
		</>
	);
}

function GitRail({ session }: { session?: WorkspaceSession }) {
	const git = useSessionGit(session?.id);
	const [message, setMessage] = useState("");
	const [description, setDescription] = useState("");
	const [discardArmed, setDiscardArmed] = useState(false);

	// Armed confirm decays on its own instead of on mouse-leave: a pixel of
	// cursor wobble silently disarming makes the second click re-arm forever.
	useEffect(() => {
		if (!discardArmed) return;
		const timer = setTimeout(() => setDiscardArmed(false), 3_000);
		return () => clearTimeout(timer);
	}, [discardArmed]);

	const files = git.status?.files ?? [];
	const branch = git.status?.branch || session?.branch || "—";

	const discard = async () => {
		// Destructive: first click arms, second click runs (mirrors the
		// context-menu confirm pattern).
		if (!discardArmed) {
			setDiscardArmed(true);
			return;
		}
		setDiscardArmed(false);
		await git.discardAll();
	};

	const commit = async () => {
		if (await git.commitAndPush(message, description)) {
			setMessage("");
			setDescription("");
		}
	};

	return (
		<>
			<SideHead title="Changed" count={files.length} />

			<div className="flex items-center gap-3 border-b border-border px-3 py-2 text-[12px]">
				<button
					className={cn(
						"inline-flex items-center gap-1.5 text-error transition-colors hover:opacity-80",
						(files.length === 0 || git.isMutating) && "pointer-events-none opacity-40",
					)}
					onClick={() => void discard()}
					type="button"
				>
					<Trash2 className="h-3 w-3" aria-hidden="true" />
					{discardArmed ? "Confirm discard" : "Discard all"}
				</button>
				<button
					className={cn(
						"ml-auto inline-flex items-center gap-1.5 text-muted-foreground transition-colors hover:text-foreground",
						(files.length === 0 || git.isMutating) && "pointer-events-none opacity-40",
					)}
					onClick={() => void git.stageAll()}
					type="button"
				>
					<Plus className="h-3 w-3" aria-hidden="true" />
					Stage all
				</button>
			</div>

			<div className="min-h-0 flex-1 overflow-y-auto p-2">
				{git.statusError ? (
					<p className="px-2 py-6 text-center text-[12px] text-passive">{git.statusError}</p>
				) : files.length === 0 ? (
					<p className="px-2 py-6 text-center text-[12px] text-passive">
						{git.isLoading ? "Loading changes…" : "No changes yet."}
					</p>
				) : (
					files.map((file) => {
						const StagedGlyph = file.staged ? SquareCheck : Square;
						return (
							<div
								className="flex h-7 items-center gap-2 rounded-md px-2 font-mono text-[12px] text-muted-foreground transition-colors hover:bg-surface"
								key={file.path}
							>
								<span className="min-w-0 flex-1 truncate text-foreground">{file.path}</span>
								<span className="shrink-0 text-success">+{file.additions}</span>
								<span className="shrink-0 text-error">−{file.deletions}</span>
								<StagedGlyph
									className={cn("h-[13px] w-[13px] shrink-0", file.staged ? "text-accent" : "text-passive")}
									aria-hidden="true"
								/>
							</div>
						);
					})
				)}
			</div>

			<div className="flex flex-col gap-2 border-t border-border p-3">
				<input
					className="w-full rounded-md border border-border bg-transparent px-2.5 py-1.5 text-[12.5px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
					onChange={(event) => setMessage(event.target.value)}
					placeholder="Commit message"
					value={message}
				/>
				<textarea
					className="w-full resize-none rounded-md border border-border bg-transparent px-2.5 py-1.5 text-[12.5px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
					onChange={(event) => setDescription(event.target.value)}
					placeholder="Description"
					rows={2}
					value={description}
				/>
				<Button
					className="w-full"
					disabled={files.length === 0 || message.trim() === "" || git.isMutating}
					onClick={() => void commit()}
					variant="primary"
				>
					{git.isMutating ? (
						<LoaderCircle className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
					) : (
						<GitCommitHorizontal className="h-3.5 w-3.5" aria-hidden="true" />
					)}
					Commit &amp; Push
				</Button>
				{git.actionError && (
					<p className="text-[11.5px] leading-snug text-error" role="alert">
						{git.actionError}
					</p>
				)}
			</div>

			<div className="flex items-center gap-2.5 border-t border-border px-3 py-2 font-mono text-[11px] text-passive">
				<GitBranch className="h-3 w-3 shrink-0" aria-hidden="true" />
				<span className="min-w-0 truncate text-muted-foreground">{branch}</span>
				<Tooltip>
					<TooltipTrigger asChild>
						<button
							className="ml-auto inline-flex shrink-0 cursor-default items-center gap-1 rounded-md border border-border px-2 py-0.5 text-muted-foreground opacity-50"
							type="button"
						>
							<Plus className="h-3 w-3" aria-hidden="true" />
							<GitPullRequest className="h-3 w-3" aria-hidden="true" />
							Create PR
						</button>
					</TooltipTrigger>
					<TooltipContent>Soon — the daemon's PR lane is still stubbed</TooltipContent>
				</Tooltip>
			</div>
		</>
	);
}

function SideHead({ title, count }: { title: string; count: number }) {
	return (
		<div className="flex h-[38px] shrink-0 items-center gap-2 border-b border-border px-3">
			<span className="text-[13px] font-semibold text-foreground">{title}</span>
			<span className="font-mono text-[11px] text-passive">{count}</span>
		</div>
	);
}
