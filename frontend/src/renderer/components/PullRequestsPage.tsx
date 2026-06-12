import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { WorkspaceSession } from "../types/workspace";
import { DashboardSubhead } from "./DashboardSubhead";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { cn } from "../lib/utils";

type PRState = NonNullable<WorkspaceSession["pullRequest"]>["state"];

const stateTone: Record<PRState, string> = {
	open: "border-success/40 bg-success/10 text-success",
	draft: "border-border bg-raised text-muted-foreground",
	merged: "border-accent/40 bg-accent-weak text-accent",
	closed: "border-error/40 bg-error/10 text-error",
};

// Order open PRs (actionable) above merged/closed.
const stateRank: Record<PRState, number> = { open: 0, draft: 1, merged: 2, closed: 3 };

type PRRow = {
	number: number;
	state: PRState;
	session: WorkspaceSession;
};

// The PR board, ported from agent-orchestrator's PullRequestsPage. The Go
// daemon has no PR-list endpoint, so the board is derived from session PR
// fields (every session carries pullRequest); actions hit /prs/{number}/merge
// and /resolve-comments. Per-PR CI/review facts live on the session route's
// inspector.
export function PullRequestsPage() {
	const navigate = useNavigate();
	const workspaceQuery = useWorkspaceQuery();
	const sessions = (workspaceQuery.data ?? []).flatMap((w) => w.sessions);
	const rows: PRRow[] = sessions
		.filter((s): s is WorkspaceSession & { pullRequest: NonNullable<WorkspaceSession["pullRequest"]> } =>
			Boolean(s.pullRequest),
		)
		.map((s) => ({ number: s.pullRequest.number, state: s.pullRequest.state, session: s }))
		.sort((a, b) => stateRank[a.state] - stateRank[b.state] || a.number - b.number);

	return (
		<div className="flex h-full min-h-0 flex-col bg-background text-foreground">
			<DashboardSubhead
				title="Pull requests"
				subtitle="Open PRs across every agent session, ready to resolve and merge."
				count={rows.length}
			/>

			<div className="min-h-0 flex-1 overflow-y-auto p-[18px]">
				{rows.length === 0 ? (
					<p className="py-10 text-center text-[12px] text-passive">No open pull requests.</p>
				) : (
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead className="w-16">PR</TableHead>
								<TableHead>Worker</TableHead>
								<TableHead className="w-24">State</TableHead>
								<TableHead className="w-[220px] text-right">Actions</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{rows.map((row) => (
								<PRRowView
									key={`${row.session.id}-${row.number}`}
									row={row}
									onOpen={() =>
										void navigate({
											to: "/projects/$projectId/sessions/$sessionId",
											params: { projectId: row.session.workspaceId, sessionId: row.session.id },
										})
									}
								/>
							))}
						</TableBody>
					</Table>
				)}
			</div>
		</div>
	);
}

function PRRowView({ row, onOpen }: { row: PRRow; onOpen: () => void }) {
	const queryClient = useQueryClient();
	const [note, setNote] = useState<{ ok: boolean; text: string } | null>(null);
	const refresh = () => void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });

	const merge = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/prs/{id}/merge", {
				params: { path: { id: String(row.number) } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: (data) => {
			setNote({ ok: true, text: `merged (${data?.method ?? "squash"})` });
			refresh();
		},
		onError: (e) => setNote({ ok: false, text: e instanceof Error ? e.message : "merge failed" }),
	});

	const resolve = useMutation({
		mutationFn: async () => {
			const { error } = await apiClient.POST("/api/v1/prs/{id}/resolve-comments", {
				params: { path: { id: String(row.number) } },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => {
			setNote({ ok: true, text: "comments resolved" });
			refresh();
		},
		onError: (e) => setNote({ ok: false, text: e instanceof Error ? e.message : "resolve failed" }),
	});

	const actionable = row.state === "open" || row.state === "draft";

	return (
		<TableRow className="cursor-pointer" onClick={onOpen}>
			<TableCell className="font-mono text-[12px] text-muted-foreground">#{row.number}</TableCell>
			<TableCell className="max-w-0">
				<div className="truncate text-[13px] text-foreground">{row.session.title}</div>
				<div className="truncate font-mono text-[10px] text-passive">
					{[row.session.workspaceName, row.session.branch].filter(Boolean).join(" · ")}
				</div>
			</TableCell>
			<TableCell>
				<Badge variant="outline" className={cn("h-5 px-1.5 text-[10px] font-medium", stateTone[row.state])}>
					{row.state}
				</Badge>
			</TableCell>
			<TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
				{note ? (
					<span className={cn("text-[11px]", note.ok ? "text-success" : "text-error")}>{note.text}</span>
				) : actionable ? (
					<div className="flex items-center justify-end gap-1.5">
						<Button
							size="sm"
							variant="ghost"
							className="h-6 px-2 text-[11px]"
							disabled={resolve.isPending}
							onClick={() => resolve.mutate()}
						>
							{resolve.isPending ? "…" : "Resolve"}
						</Button>
						<Button
							size="sm"
							variant="primary"
							className="h-6 px-2 text-[11px]"
							disabled={merge.isPending}
							onClick={() => merge.mutate()}
						>
							{merge.isPending ? "Merging…" : "Merge"}
						</Button>
					</div>
				) : (
					<span className="text-[11px] text-passive">—</span>
				)}
			</TableCell>
		</TableRow>
	);
}
