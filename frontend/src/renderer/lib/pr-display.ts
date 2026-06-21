import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import {
	sortedPRs,
	type PRState,
	type PullRequestFacts,
	type WorkspaceSession,
} from "../types/workspace";

const prStateRank: Record<PRState, number> = { open: 0, draft: 1, merged: 2, closed: 3 };
const ciStates = new Set<SessionPRSummary["ci"]["state"]>(["unknown", "pending", "passing", "failing"]);
const reviewDecisions = new Set<SessionPRSummary["review"]["decision"]>([
	"none",
	"approved",
	"changes_requested",
	"review_required",
]);
const mergeabilityStates = new Set<SessionPRSummary["mergeability"]["state"]>([
	"unknown",
	"mergeable",
	"conflicting",
	"blocked",
	"unstable",
]);

export function comparePRDisplaySummaries(a: SessionPRSummary, b: SessionPRSummary): number {
	return prStateRank[a.state] - prStateRank[b.state] || a.number - b.number;
}

export function sessionPRDisplaySummaries(
	session: WorkspaceSession,
	summaries: SessionPRSummary[] = [],
): SessionPRSummary[] {
	const summariesByNumber = new Map(summaries.map((summary) => [summary.number, summary]));
	const seen = new Set<number>();
	const fromFacts = sortedPRs(session).map((pr) => {
		seen.add(pr.number);
		return summariesByNumber.get(pr.number) ?? sessionPRFactToSummary(session, pr);
	});
	const summaryOnly = summaries.filter((summary) => !seen.has(summary.number));
	return [...fromFacts, ...summaryOnly].sort(comparePRDisplaySummaries);
}

function sessionPRFactToSummary(session: WorkspaceSession, pr: PullRequestFacts): SessionPRSummary {
	return {
		url: pr.url,
		htmlUrl: pr.url,
		number: pr.number,
		title: session.title,
		state: pr.state,
		provider: "github",
		repo: session.workspaceName,
		author: "",
		sourceBranch: session.branch,
		targetBranch: "",
		headSha: "",
		ci: {
			state: toCIState(pr.ci),
			failingChecks: [],
		},
		review: {
			decision: toReviewDecision(pr.review),
			hasUnresolvedHumanComments: pr.reviewComments,
			unresolvedBy: [],
		},
		mergeability: {
			state: toMergeabilityState(pr.mergeability),
			reasons: [],
			prUrl: pr.url,
			conflictFiles: [],
		},
		updatedAt: pr.updatedAt,
		observedAt: pr.updatedAt,
		ciObservedAt: pr.updatedAt,
		reviewObservedAt: pr.updatedAt,
	};
}

function toCIState(value: string): SessionPRSummary["ci"]["state"] {
	return ciStates.has(value as SessionPRSummary["ci"]["state"])
		? (value as SessionPRSummary["ci"]["state"])
		: "unknown";
}

function toReviewDecision(value: string): SessionPRSummary["review"]["decision"] {
	return reviewDecisions.has(value as SessionPRSummary["review"]["decision"])
		? (value as SessionPRSummary["review"]["decision"])
		: "none";
}

function toMergeabilityState(value: string): SessionPRSummary["mergeability"]["state"] {
	return mergeabilityStates.has(value as SessionPRSummary["mergeability"]["state"])
		? (value as SessionPRSummary["mergeability"]["state"])
		: "unknown";
}
