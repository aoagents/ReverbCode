import {
	Columns2,
	FileText,
	GitPullRequest,
	MoreHorizontal,
	PanelLeft,
	Pin,
	Plus,
	Terminal,
	Waypoints,
} from "lucide-react";
import type { WorkbenchTab, WorkbenchView } from "../stores/ui-store";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { workerDisplayStatus } from "../types/workspace";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { cn } from "../lib/utils";

const isMac = typeof navigator !== "undefined" && /Mac|iPod|iPhone|iPad/.test(navigator.userAgent);
const dragStyle = isMac ? ({ WebkitAppRegion: "drag" } as React.CSSProperties) : undefined;
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

type TopbarProps = {
	view: WorkbenchView;
	session?: WorkspaceSession;
	workspace?: WorkspaceSummary;
	workbenchTab: WorkbenchTab;
	onSetWorkbenchTab: (tab: WorkbenchTab) => void;
	onNewWorker: () => void;
	onToggleSidebar: () => void;
};

export function Topbar({
	view,
	session,
	workspace,
	workbenchTab,
	onSetWorkbenchTab,
	onNewWorker,
	onToggleSidebar,
}: TopbarProps) {
	return (
		<header
			className="flex h-11 shrink-0 items-center gap-2.5 border-b border-border bg-background px-3"
			style={dragStyle}
		>
			{isMac && <span className="inline-block w-[66px] shrink-0" />}
			<button
				aria-label="Toggle sidebar"
				className="grid h-7 w-7 shrink-0 place-items-center rounded-md text-passive transition-colors hover:bg-raised hover:text-muted-foreground"
				onClick={onToggleSidebar}
				style={noDragStyle}
				title="Toggle sidebar (⌘B)"
				type="button"
			>
				<PanelLeft className="h-[15px] w-[15px]" aria-hidden="true" />
			</button>

			{view === "orchestrator" ? (
				<div className="flex min-w-0 items-center gap-2 text-[13px] text-muted-foreground">
					<Waypoints className="h-[15px] w-[15px] shrink-0 text-accent" aria-hidden="true" />
					<span className="truncate font-medium text-foreground">Orchestrator</span>
				</div>
			) : (
				<div className="flex min-w-0 items-center gap-1.5 text-[13px] text-muted-foreground">
					<span className="truncate">{session?.workspaceName ?? workspace?.name ?? "—"}</span>
					<span className="text-passive">/</span>
					<span className="truncate font-medium text-foreground">{session?.title ?? "session"}</span>
					<Pin className="h-3 w-3 shrink-0 text-passive" aria-hidden="true" />
				</div>
			)}

			<div className="ml-auto flex shrink-0 items-center gap-0.5">
				{view === "orchestrator" ? (
					<>
						<button
							aria-label="New worker"
							className="mr-1 inline-flex h-6 items-center gap-1.5 rounded-md border border-border px-2.5 text-[11.5px] text-muted-foreground transition-colors hover:border-accent hover:text-accent"
							onClick={onNewWorker}
							style={noDragStyle}
							type="button"
						>
							<Plus className="h-3 w-3" aria-hidden="true" />
							New worker
						</button>
						<IconToggle label="Terminal" active>
							<Terminal className="h-[15px] w-[15px]" aria-hidden="true" />
						</IconToggle>
						<IconToggle label="More">
							<MoreHorizontal className="h-[15px] w-[15px]" aria-hidden="true" />
						</IconToggle>
					</>
				) : (
					<>
						<PrPill session={session} workspace={workspace} />
						<IconToggle
							label="Changes"
							active={workbenchTab === "changes"}
							onClick={() => onSetWorkbenchTab("changes")}
						>
							<Columns2 className="h-[15px] w-[15px]" aria-hidden="true" />
						</IconToggle>
						<IconToggle label="Files" active={workbenchTab === "files"} onClick={() => onSetWorkbenchTab("files")}>
							<FileText className="h-[15px] w-[15px]" aria-hidden="true" />
						</IconToggle>
						<IconToggle
							label="Terminal"
							active={workbenchTab === "terminal"}
							onClick={() => onSetWorkbenchTab("terminal")}
						>
							<Terminal className="h-[15px] w-[15px]" aria-hidden="true" />
						</IconToggle>
						<IconToggle label="Session actions">
							<MoreHorizontal className="h-[15px] w-[15px]" aria-hidden="true" />
						</IconToggle>
					</>
				)}
			</div>
		</header>
	);
}

function IconToggle({
	label,
	active = false,
	onClick,
	children,
}: {
	label: string;
	active?: boolean;
	onClick?: () => void;
	children: React.ReactNode;
}) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					aria-label={label}
					aria-pressed={active}
					className={cn(
						"grid h-7 w-7 place-items-center rounded-md transition-colors",
						active ? "bg-accent-weak text-accent" : "text-passive hover:bg-raised hover:text-muted-foreground",
					)}
					onClick={onClick}
					style={noDragStyle}
					type="button"
				>
					{children}
				</button>
			</TooltipTrigger>
			<TooltipContent>{label}</TooltipContent>
		</Tooltip>
	);
}

function PrPill({ session, workspace }: { session?: WorkspaceSession; workspace?: WorkspaceSummary }) {
	const pr = session?.pullRequest ?? workspace?.pullRequest;
	const status = session ? workerDisplayStatus(session) : "working";

	if (!pr) {
		return (
			<button
				className="mr-1 inline-flex h-6 items-center gap-1.5 rounded-md border border-border px-2.5 text-[11.5px] font-medium text-muted-foreground transition-colors hover:border-accent hover:text-accent"
				style={noDragStyle}
				type="button"
			>
				<GitPullRequest className="h-3 w-3" aria-hidden="true" />
				Open PR
			</button>
		);
	}

	const tone =
		status === "ci_failed"
			? "border-error/40 bg-error/10 text-error"
			: status === "needs_you"
				? "border-warning/40 bg-warning/10 text-warning"
				: "border-success/40 bg-success/10 text-success";
	const label = status === "ci_failed" ? "CI failed" : status === "needs_you" ? "review requested" : "mergeable";

	return (
		<button
			className={cn(
				"mr-1 inline-flex h-6 items-center gap-1.5 whitespace-nowrap rounded-md border px-2.5 text-[11.5px] font-medium",
				tone,
			)}
			style={noDragStyle}
			title={`PR #${pr.number} — ${label}`}
			type="button"
		>
			<GitPullRequest className="h-3 w-3" aria-hidden="true" />
			PR #{pr.number} · {label}
		</button>
	);
}
