import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { AlertCircle, CheckCircle2, CircleMinus, GitPullRequest, Play, Shield, Terminal } from "lucide-react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { formatTimeCompact } from "../lib/format-time";
import type { PRState, PullRequestFacts, SessionStatus, WorkspaceSession } from "../types/workspace";
import { sortedPRs, workerDisplayStatus } from "../types/workspace";
import { Badge } from "./ui/badge";
import { cn } from "../lib/utils";

type ProjectConfig = components["schemas"]["ProjectConfig"];
type ReviewRun = components["schemas"]["ReviewRun"];
type ReviewsResponse = components["schemas"]["ListReviewsResponse"];
type OpenReviewerTerminal = (target: { handleId: string; harness: string }) => void;

type InspectorView = "summary" | "reviews" | "browser";

const VIEWS: { id: InspectorView; label: string; icon: ReactNode }[] = [
	{
		id: "summary",
		label: "Summary",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<line x1="8" y1="7" x2="20" y2="7" />
				<line x1="8" y1="12" x2="20" y2="12" />
				<line x1="8" y1="17" x2="16" y2="17" />
				<circle cx="4" cy="7" r="1" />
				<circle cx="4" cy="12" r="1" />
				<circle cx="4" cy="17" r="1" />
			</svg>
		),
	},
	{
		id: "reviews",
		label: "Reviews",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z" />
			</svg>
		),
	},
	{
		id: "browser",
		label: "Browser",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<circle cx="12" cy="12" r="9" />
				<line x1="3" y1="12" x2="21" y2="12" />
				<path d="M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18" />
			</svg>
		),
	},
];

const prStateTone: Record<PRState, string> = {
	open: "border-success/40 bg-success/10 text-success",
	draft: "border-border bg-raised text-muted-foreground",
	merged: "border-accent/40 bg-accent-weak text-accent",
	closed: "border-error/40 bg-error/10 text-error",
};

/**
 * Tabbed inspector rail beside the terminal (Summary · Reviews · Browser).
 */
export function SessionInspector({
	session,
	onOpenReviewerTerminal,
}: {
	session?: WorkspaceSession;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
}) {
	const [view, setView] = useState<InspectorView>("summary");

	if (!session) {
		return (
			<aside className="session-inspector" aria-label="Session inspector">
				<div className="session-inspector__body">
					<p className="inspector-empty">Loading session…</p>
				</div>
			</aside>
		);
	}

	return (
		<aside className="session-inspector" aria-label="Session inspector">
			<div className="session-inspector__tabs" role="tablist">
				{VIEWS.map((entry) => (
					<button
						key={entry.id}
						type="button"
						role="tab"
						aria-selected={view === entry.id}
						className={cn("session-inspector__tab", view === entry.id && "is-active")}
						onClick={() => setView(entry.id)}
					>
						<span className="session-inspector__tab-icon">{entry.icon}</span>
						<span className="session-inspector__tab-label">{entry.label}</span>
					</button>
				))}
			</div>

			<div className="session-inspector__body">
				{view === "summary" ? <SummaryView session={session} /> : null}
				{view === "reviews" ? <ReviewsView onOpenReviewerTerminal={onOpenReviewerTerminal} session={session} /> : null}
				{view === "browser" ? <BrowserView /> : null}
			</div>
		</aside>
	);
}

function Section({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
	return (
		<section className="inspector-section">
			<div className="inspector-section__head">
				<span>{title}</span>
				{action ?? null}
			</div>
			{children}
		</section>
	);
}

function SummaryView({ session }: { session: WorkspaceSession }) {
	const prs = sortedPRs(session);
	const branchLabel = session.branch || `session/${session.id}`;

	return (
		<div role="tabpanel">
			<Section title={prs.length > 1 ? `Pull requests (${prs.length})` : "Pull request"}>
				{prs.length === 0 ? (
					<p className="inspector-empty">No pull request opened yet.</p>
				) : (
					<div className="flex flex-col gap-2.5">
						{prs.map((pr) => (
							<PRCard key={pr.url} pr={pr} />
						))}
					</div>
				)}
			</Section>

			<Section title="Activity">
				<ActivityTimeline session={session} />
			</Section>

			<Section title="Overview">
				<dl className="inspector-kv">
					<Row k="Agent" v={session.provider} mono />
					<Row k="Branch" v={branchLabel} mono />
					<Row k="Started" v={formatTimeCompact(session.createdAt ?? session.updatedAt)} mono />
					<Row k="Session" v={session.id} mono />
				</dl>
			</Section>
		</div>
	);
}

// One PR per card; a session's PRs stack vertically. Mirrors the minimal
// single-PR rail the parallel-agent tools converged on (emdash, conductor),
// repeated per PR rather than collapsed into one aggregate widget.
function PRCard({ pr }: { pr: PullRequestFacts }) {
	return (
		<div className="flex flex-col gap-2 rounded-[7px] border border-border bg-surface p-2.5">
			<div className="flex items-center gap-2">
				<GitPullRequest className="h-3.5 w-3.5 shrink-0 text-passive" aria-hidden="true" />
				<span className="text-[12.5px] font-medium text-foreground">PR #{pr.number}</span>
				<Badge variant="outline" className={cn("ml-auto h-5 px-1.5 text-[10px] font-medium", prStateTone[pr.state])}>
					{pr.state}
				</Badge>
				{pr.url ? (
					<a href={pr.url} target="_blank" rel="noopener noreferrer" className="inspector-section__link">
						Open ↗
					</a>
				) : null}
			</div>
			<dl className="inspector-kv">
				<Row k="CI" v={pr.ci || "—"} mono />
				<Row k="Merge" v={pr.mergeability || "—"} mono />
				<Row k="Review" v={pr.review || "—"} mono />
			</dl>
		</div>
	);
}

type TimelineTone = "now" | "good" | "warn" | "neutral";

function ActivityTimeline({ session }: { session: WorkspaceSession }) {
	const events: { tone: TimelineTone; node: ReactNode; ts: string | null }[] = [];
	const detail = activityDetail(session.status);

	events.push({
		tone: "now",
		node: (
			<>
				<span className="inspector-timeline__badge">
					<InspectorStatusPill session={session} />
				</span>
				{detail ? <span className="inspector-timeline__detail"> — {detail}</span> : null}
			</>
		),
		ts: formatTimeCompact(session.updatedAt),
	});

	for (const pr of sortedPRs(session)) {
		events.push({
			tone: "good",
			node: (
				<>
					Opened <b>PR #{pr.number}</b>
				</>
			),
			ts: null,
		});
	}

	events.push({
		tone: "neutral",
		node: <>Created worktree &amp; branch</>,
		ts: formatTimeCompact(session.createdAt ?? session.updatedAt),
	});

	return (
		<div className="inspector-timeline">
			{events.map((event, index) => (
				<div
					key={index}
					className={cn(
						"inspector-timeline__ev",
						event.tone === "now" && "inspector-timeline__ev--now",
						event.tone === "good" && "inspector-timeline__ev--good",
						event.tone === "warn" && "inspector-timeline__ev--warn",
					)}
				>
					<span className="inspector-timeline__node" aria-hidden="true" />
					<div className="inspector-timeline__et">{event.node}</div>
					{event.ts ? <div className="inspector-timeline__ets">{event.ts}</div> : null}
				</div>
			))}
		</div>
	);
}

function activityDetail(status: SessionStatus): string | null {
	switch (status) {
		case "idle":
			return "Session idle";
		case "needs_input":
			return "Waiting for your input";
		case "no_signal":
			return "No recent agent signal";
		case "working":
			return null;
		default:
			return null;
	}
}

const STATUS_PILL: Record<
	ReturnType<typeof workerDisplayStatus> | "idle",
	{ label: string; tone: string; breathe: boolean }
> = {
	working: { label: "Working", tone: "var(--orange)", breathe: true },
	needs_you: { label: "Input needed", tone: "var(--amber)", breathe: false },
	ci_failed: { label: "CI failed", tone: "var(--red)", breathe: false },
	no_signal: { label: "No signal", tone: "var(--fg-muted)", breathe: false },
	mergeable: { label: "Ready", tone: "var(--green)", breathe: false },
	done: { label: "Done", tone: "var(--fg-muted)", breathe: false },
	idle: { label: "Idle", tone: "var(--fg-muted)", breathe: false },
};

function InspectorStatusPill({ session }: { session: WorkspaceSession }) {
	const key = session.status === "idle" ? "idle" : workerDisplayStatus(session);
	const { label, tone, breathe } = STATUS_PILL[key];
	return (
		<span
			className="inline-flex shrink-0 items-center gap-[7px] whitespace-nowrap rounded-[7px] px-[11px] py-[5px] text-[11.5px] font-semibold"
			style={{
				color: tone,
				background: `color-mix(in srgb, ${tone} 7%, transparent)`,
				boxShadow: `inset 0 0 0 1px color-mix(in srgb, ${tone} 25%, transparent)`,
			}}
		>
			<span
				className={cn("h-1.5 w-1.5 rounded-full", breathe && "animate-status-pulse")}
				style={{ background: tone }}
			/>
			{label}
		</span>
	);
}

function ReviewsView({
	session,
	onOpenReviewerTerminal,
}: {
	session: WorkspaceSession;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
}) {
	const hasPr = sortedPRs(session).length > 0;
	const queryClient = useQueryClient();
	const [reviewNotice, setReviewNotice] = useState<string | null>(null);
	const reviewsQuery = useQuery({
		queryKey: ["session-reviews", session.id],
		enabled: hasPr,
		refetchInterval: (query) => {
			const data = query.state.data as ReviewsResponse | undefined;
			return data?.reviews.some((review) => review.status === "running") ? 2500 : false;
		},
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/reviews", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load reviews"));
			return data ?? ({ reviewerHandleId: "", reviews: [] } satisfies ReviewsResponse);
		},
	});
	const projectConfigQuery = useQuery({
		queryKey: ["project-config", session.workspaceId],
		enabled: hasPr,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", {
				params: { path: { id: session.workspaceId } },
			});
			if (error) return undefined;
			return projectConfig(data?.project);
		},
	});
	const triggerReview = useMutation({
		mutationFn: async () => {
			const { data, error, response } = await apiClient.POST("/api/v1/sessions/{sessionId}/reviews/trigger", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to start review"));
			return { data, reused: response?.status === 200 };
		},
		onMutate: () => {
			setReviewNotice(null);
		},
		onSuccess: ({ data, reused }) => {
			void queryClient.invalidateQueries({ queryKey: ["session-reviews", session.id] });
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			if (reused) {
				setReviewNotice("Review is already up to date for this commit.");
				return;
			}
			if (data?.reviewerHandleId) {
				onOpenReviewerTerminal?.({ handleId: data.reviewerHandleId, harness: data.review.harness || "reviewer" });
			}
		},
	});
	const reviews = reviewsQuery.data?.reviews ?? [];

	return (
		<div role="tabpanel">
			<Section title="Reviews">
				<ReviewPanel
					config={projectConfigQuery.data}
					error={reviewsQuery.error ?? triggerReview.error}
					isLoading={reviewsQuery.isLoading}
					isTriggering={triggerReview.isPending}
					onOpenTerminal={onOpenReviewerTerminal}
					onTrigger={() => triggerReview.mutate()}
					reviewerHandleId={reviewsQuery.data?.reviewerHandleId ?? ""}
					reviews={reviews}
					notice={reviewNotice}
					session={session}
				/>
			</Section>
		</div>
	);
}

function projectConfig(project: components["schemas"]["ProjectOrDegraded"] | undefined): ProjectConfig | undefined {
	if (!project || !("config" in project)) return undefined;
	return project.config;
}

function ReviewPanel({
	session,
	config,
	reviews,
	reviewerHandleId,
	isLoading,
	isTriggering,
	error,
	notice,
	onTrigger,
	onOpenTerminal,
}: {
	session: WorkspaceSession;
	config?: ProjectConfig;
	reviews: ReviewRun[];
	reviewerHandleId: string;
	isLoading: boolean;
	isTriggering: boolean;
	error: unknown;
	notice: string | null;
	onTrigger: () => void;
	onOpenTerminal?: OpenReviewerTerminal;
}) {
	if (sortedPRs(session).length === 0) {
		return <p className="inspector-empty">No pull request opened yet.</p>;
	}
	if (isLoading) {
		return <p className="inspector-empty">Loading reviews...</p>;
	}

	const latest = latestReview(reviews);
	const harness = latest?.harness || config?.reviewers?.[0]?.harness || session.provider || "reviewer";

	return (
		<div className="reviewer-list">
			{error ? <p className="reviewer-error">{apiErrorMessage(error, "Review request failed")}</p> : null}
			{notice ? <p className="reviewer-notice">{notice}</p> : null}
			<ReviewerCard
				handleId={reviewerHandleId}
				harness={harness}
				isTriggering={isTriggering}
				onOpenTerminal={onOpenTerminal}
				onTrigger={onTrigger}
				review={latest}
			/>
		</div>
	);
}

function latestReview(reviews: ReviewRun[]): ReviewRun | undefined {
	return [...reviews].sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))[0];
}

function ReviewerCard({
	harness,
	review,
	handleId,
	isTriggering,
	onTrigger,
	onOpenTerminal,
}: {
	harness: string;
	review?: ReviewRun;
	handleId: string;
	isTriggering: boolean;
	onTrigger: () => void;
	onOpenTerminal?: OpenReviewerTerminal;
}) {
	const status = reviewStatus(review);
	const terminalEnabled = Boolean(handleId && onOpenTerminal);
	const runLabel = review ? "Re-run review" : "Run review";

	return (
		<div className={cn("reviewer-card", status.tone && `reviewer-card--${status.tone}`)}>
			<div className="reviewer-card__top">
				<div className="reviewer-card__name">
					<Shield aria-hidden="true" />
					<span>{harness}</span>
				</div>
				<span className={cn("reviewer-status", `reviewer-status--${status.tone}`)}>
					{status.icon}
					{status.label}
				</span>
			</div>
			<div className="reviewer-card__actions">
				<button
					className="reviewer-card__action reviewer-card__action--primary"
					disabled={isTriggering}
					onClick={onTrigger}
					type="button"
				>
					<Play aria-hidden="true" />
					{isTriggering ? "Starting..." : runLabel}
				</button>
				{review ? (
					<button
						className="reviewer-card__action"
						disabled={!terminalEnabled}
						onClick={() => {
							if (!terminalEnabled) return;
							onOpenTerminal?.({ handleId, harness });
						}}
						type="button"
					>
						<Terminal aria-hidden="true" />
						Open terminal
					</button>
				) : null}
			</div>
		</div>
	);
}

function reviewStatus(review?: ReviewRun): {
	label: string;
	tone: "neutral" | "running" | "success" | "danger";
	icon: ReactNode;
} {
	if (!review) return { label: "Not run", tone: "neutral", icon: null };
	if (review.status === "running") {
		return { label: "Running", tone: "running", icon: <Play aria-hidden="true" /> };
	}
	if (review.status === "failed") {
		return { label: "Failed", tone: "danger", icon: <AlertCircle aria-hidden="true" /> };
	}
	if (review.verdict === "approved") {
		return { label: "Approved", tone: "success", icon: <CheckCircle2 aria-hidden="true" /> };
	}
	if (review.verdict === "changes_requested") {
		return { label: "Changes requested", tone: "danger", icon: <CircleMinus aria-hidden="true" /> };
	}
	return { label: "Complete", tone: "success", icon: <CheckCircle2 aria-hidden="true" /> };
}

function BrowserView() {
	return (
		<div role="tabpanel">
			<div className="inspector-empty inspector-empty--browser">
				<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
					<circle cx="12" cy="12" r="9" />
					<line x1="3" y1="12" x2="21" y2="12" />
					<path d="M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18" />
				</svg>
				<p>No live browser preview.</p>
				<span>A browser plugin will render what the agent is viewing here.</span>
			</div>
		</div>
	);
}

function Row({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
	return (
		<div className="inspector-kv__row">
			<dt className="inspector-kv__k">{k}</dt>
			<dd className={cn("inspector-kv__v", mono && "inspector-kv__v--mono")}>{v}</dd>
		</div>
	);
}
