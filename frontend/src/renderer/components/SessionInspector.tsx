import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import {
	AlertCircle,
	CheckCircle2,
	CircleMinus,
	GitBranch,
	GitCommitHorizontal,
	GitPullRequest,
	Play,
	Plus,
	Shield,
	Square,
	Terminal,
	Trash2,
} from "lucide-react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { formatTimeCompact } from "../lib/format-time";
import type { SessionStatus, WorkspaceSession } from "../types/workspace";
import { workerDisplayStatus } from "../types/workspace";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { cn } from "../lib/utils";

type PRFacts = components["schemas"]["SessionPRFacts"];
type ProjectConfig = components["schemas"]["ProjectConfig"];
type ReviewRun = components["schemas"]["ReviewRun"];
type ReviewsResponse = components["schemas"]["ListReviewsResponse"];
type InspectorView = "summary" | "changes" | "browser";

type OpenReviewerTerminal = (target: { handleId: string; harness: string }) => void;

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
		id: "changes",
		label: "Changes",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<path d="M12 3v6" />
				<path d="M9 6h6" />
				<path d="M11 18H7a2 2 0 0 1-2-2V6" />
				<path d="M13 15h4" />
				<path d="M19 9v7a2 2 0 0 1-2 2h-2" />
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

const prStateTone: Record<PRFacts["state"], string> = {
	open: "border-success/40 bg-success/10 text-success",
	draft: "border-border bg-raised text-muted-foreground",
	merged: "border-accent/40 bg-accent-weak text-accent",
	closed: "border-error/40 bg-error/10 text-error",
};

/**
 * Tabbed inspector rail beside the terminal — cloned from agent-orchestrator
 * SessionInspector (Summary · Changes · Browser).
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
				{view === "summary" ? <SummaryView onOpenReviewerTerminal={onOpenReviewerTerminal} session={session} /> : null}
				{view === "changes" ? <ChangesView session={session} /> : null}
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

function SummaryView({
	session,
	onOpenReviewerTerminal,
}: {
	session: WorkspaceSession;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
}) {
	const hasPr = Boolean(session.pullRequest);
	const queryClient = useQueryClient();
	const [reviewNotice, setReviewNotice] = useState<string | null>(null);
	const query = useQuery({
		queryKey: ["session-pr", session.id],
		enabled: hasPr,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/pr", {
				params: { path: { sessionId: session.id } },
			});
			if (error) return [] as PRFacts[];
			return data?.prs ?? [];
		},
	});
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
			void queryClient.invalidateQueries({ queryKey: ["session-pr", session.id] });
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
	const prFacts = query.data?.[0];
	const branchLabel = session.branch || `session/${session.id}`;
	const reviews = reviewsQuery.data?.reviews ?? [];

	return (
		<div role="tabpanel">
			<Section
				title="Pull request"
				action={
					prFacts?.url ? (
						<a href={prFacts.url} target="_blank" rel="noopener noreferrer" className="inspector-section__link">
							Open ↗
						</a>
					) : undefined
				}
			>
				{!hasPr ? (
					<p className="inspector-empty">No pull request opened yet.</p>
				) : query.isLoading ? (
					<p className="inspector-empty">Loading pull request…</p>
				) : (
					<div className="flex flex-col gap-2">
						<div className="inspector-pr-summary">
							<GitPullRequest className="h-3.5 w-3.5 shrink-0 text-passive" aria-hidden="true" />
							<span className="inspector-pr-summary__title">PR #{prFacts?.number ?? session.pullRequest?.number}</span>
							{prFacts ? (
								<Badge
									variant="outline"
									className={cn(
										"inspector-pr-summary__state h-5 px-1.5 text-[10px] font-medium",
										prStateTone[prFacts.state],
									)}
								>
									{prFacts.state}
								</Badge>
							) : null}
						</div>
						{prFacts ? (
							<dl className="inspector-kv">
								<Row k="CI" v={prFacts.ci || "—"} mono />
								<Row k="Merge" v={prFacts.mergeability || "—"} mono />
								<Row k="Review" v={prFacts.review || "—"} mono />
							</dl>
						) : (
							<p className="inspector-empty">No enriched PR facts yet.</p>
						)}
					</div>
				)}
			</Section>

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

			<Section title="Activity">
				<ActivityTimeline reviews={reviews} session={session} />
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
	if (!session.pullRequest) {
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

type TimelineTone = "now" | "good" | "warn" | "bad" | "neutral";

function ActivityTimeline({ session, reviews }: { session: WorkspaceSession; reviews: ReviewRun[] }) {
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

	if (session.pullRequest) {
		events.push({
			tone: "good",
			node: (
				<>
					Opened <b>PR #{session.pullRequest.number}</b>
				</>
			),
			ts: null,
		});
	}

	for (const review of reviews.slice(0, 4)) {
		events.push({
			tone: reviewTimelineTone(review),
			node: (
				<>
					{reviewTimelineLabel(review)} <span className="inspector-timeline__detail">- {review.harness}</span>
				</>
			),
			ts: formatTimeCompact(review.createdAt),
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
						event.tone === "bad" && "inspector-timeline__ev--bad",
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

function reviewTimelineTone(review: ReviewRun): TimelineTone {
	if (review.status === "failed" || review.verdict === "changes_requested") return "bad";
	if (review.status === "running") return "warn";
	if (review.verdict === "approved") return "good";
	return "neutral";
}

function reviewTimelineLabel(review: ReviewRun): string {
	if (review.status === "running") return "Review requested";
	if (review.status === "failed") return "Review failed";
	if (review.verdict === "approved") return "Approved";
	if (review.verdict === "changes_requested") return "Changes requested";
	return "Review complete";
}

function activityDetail(status: SessionStatus): string | null {
	switch (status) {
		case "idle":
			return "Session idle";
		case "needs_input":
			return "Waiting for your input";
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

function ChangesView({ session }: { session: WorkspaceSession }) {
	const files = session.changedFiles ?? [];

	return (
		<div role="tabpanel" className="flex min-h-0 flex-1 flex-col">
			<div className="inspector-changes__head">
				<span className="inspector-changes__title">Changed</span>
				<span className="inspector-changes__count">{files.length}</span>
			</div>

			<div className="inspector-changes__actions">
				<button className="inspector-changes__action" type="button">
					All files
				</button>
				<button className="inspector-changes__action inspector-changes__action--danger" type="button">
					<Trash2 aria-hidden="true" />
					Discard all
				</button>
				<button className="inspector-changes__action inspector-changes__action--end" type="button">
					<Plus aria-hidden="true" />
					Stage all
				</button>
			</div>

			<div className="inspector-changes__list">
				{files.length === 0 ? (
					<p className="inspector-empty inspector-empty--center">No changes yet.</p>
				) : (
					files.map((file) => (
						<div className="inspector-changes__file" key={file.path}>
							<span className="inspector-changes__path">{file.path}</span>
							<span className="inspector-changes__add">+{file.additions}</span>
							<span className="inspector-changes__del">−{file.deletions}</span>
							<Square className={cn("inspector-changes__stage", file.staged && "is-staged")} aria-hidden="true" />
						</div>
					))
				)}
			</div>

			<div className="inspector-changes__commit">
				<input
					className="inspector-changes__input"
					defaultValue={session.commitMessage ?? ""}
					key={session.id}
					placeholder="Commit message"
				/>
				<textarea className="inspector-changes__textarea" placeholder="Description" rows={2} />
				<Button className="w-full" disabled={files.length === 0} variant="primary">
					<GitCommitHorizontal aria-hidden="true" />
					Commit &amp; Push
				</Button>
			</div>

			<div className="inspector-changes__footer">
				<GitBranch aria-hidden="true" />
				<span className="inspector-changes__branch">{session.branch || "—"}</span>
				<button className="inspector-changes__pr" type="button">
					<Plus aria-hidden="true" />
					<GitPullRequest aria-hidden="true" />
					Create PR
				</button>
			</div>
		</div>
	);
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
