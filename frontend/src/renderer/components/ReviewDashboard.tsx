import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Play } from "lucide-react";
import { useState } from "react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { DashboardSubhead, DashboardTopbar } from "./DashboardTopbar";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Card, CardContent } from "./ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { cn } from "../lib/utils";

type ReviewRun = components["schemas"]["ReviewRun"];

const reviewsKey = ["reviews"] as const;

const statusTone: Record<string, string> = {
	pending: "border-warning/40 bg-warning/10 text-warning",
	complete: "border-success/40 bg-success/10 text-success",
	sent: "border-accent/40 bg-accent-weak text-accent",
};

// The code-review board, ported from agent-orchestrator's ReviewDashboard onto
// the daemon's reviews API (GET/POST /api/v1/reviews). Lists review runs and
// their findings; lets you start a run for a session and send its findings.
export function ReviewDashboard() {
	const queryClient = useQueryClient();
	const sessions = (useWorkspaceQuery().data ?? []).flatMap((w) => w.sessions);
	const [target, setTarget] = useState<string>("");

	const reviews = useQuery({
		queryKey: reviewsKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/reviews");
			if (error) throw new Error(apiErrorMessage(error));
			return data?.reviews ?? [];
		},
	});

	const execute = useMutation({
		mutationFn: async (sessionId: string) => {
			const { error } = await apiClient.POST("/api/v1/reviews/execute", { body: { sessionId } });
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: reviewsKey }),
	});

	const runs = (reviews.data ?? []).slice().sort((a, b) => b.createdAt.localeCompare(a.createdAt));

	return (
		<div className="flex h-full min-h-0 flex-col bg-background text-foreground">
			<DashboardTopbar activeTab="reviews" />
			<DashboardSubhead
				title="Reviews"
				subtitle="Code-review runs and their findings, ready to send back."
				count={runs.length}
			/>
			<div className="flex items-center gap-2 px-[18px] pt-3">
				<Select value={target} onValueChange={setTarget}>
					<SelectTrigger className="h-8 w-56 text-[12px]">
						<SelectValue placeholder="Select a worker…" />
					</SelectTrigger>
					<SelectContent>
						{sessions.length === 0 ? (
							<SelectItem value="__none__" disabled>
								No workers
							</SelectItem>
						) : (
							sessions.map((s) => (
								<SelectItem key={s.id} value={s.id}>
									{s.title}
								</SelectItem>
							))
						)}
					</SelectContent>
				</Select>
				<Button
					size="sm"
					variant="primary"
					className="h-8 px-3 text-[12px]"
					disabled={!target || target === "__none__" || execute.isPending}
					onClick={() => execute.mutate(target)}
				>
					<Play className="h-3 w-3" aria-hidden="true" />
					{execute.isPending ? "Starting…" : "Run review"}
				</Button>
			</div>

			<div className="min-h-0 flex-1 overflow-y-auto p-[18px]">
				{reviews.isError ? (
					<p className="py-10 text-center text-[12px] text-passive">Could not load reviews.</p>
				) : runs.length === 0 ? (
					<p className="py-10 text-center text-[12px] text-passive">
						No review runs yet. Pick a worker and <span className="text-foreground">Run review</span>.
					</p>
				) : (
					<div className="mx-auto flex max-w-2xl flex-col gap-2.5">
						{runs.map((run) => (
							<ReviewRunCard
								key={run.id}
								run={run}
								sessionTitle={sessions.find((s) => s.id === run.sessionId)?.title}
							/>
						))}
					</div>
				)}
			</div>
		</div>
	);
}

function ReviewRunCard({ run, sessionTitle }: { run: ReviewRun; sessionTitle?: string }) {
	const queryClient = useQueryClient();
	const send = useMutation({
		mutationFn: async () => {
			const { error } = await apiClient.POST("/api/v1/reviews/{id}/send", { params: { path: { id: run.id } } });
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: reviewsKey }),
	});

	return (
		<Card className="gap-0 py-0">
			<CardContent className="flex flex-col gap-2 p-3">
				<div className="flex items-center gap-2">
					<span className="font-mono text-[12px] text-muted-foreground">{run.id}</span>
					<span className="min-w-0 flex-1 truncate text-[13px] text-foreground">{sessionTitle ?? run.sessionId}</span>
					<Badge variant="outline" className={cn("h-5 px-1.5 text-[10px] font-medium", statusTone[run.status])}>
						{run.status}
					</Badge>
					{run.status !== "sent" && (
						<Button
							size="sm"
							variant="ghost"
							className="h-6 px-2 text-[11px]"
							disabled={send.isPending}
							onClick={() => send.mutate()}
						>
							{send.isPending ? "Sending…" : "Send"}
						</Button>
					)}
				</div>
				{run.findings.length === 0 ? (
					<p className="font-mono text-[11px] text-passive">no findings</p>
				) : (
					<ul className="flex flex-col gap-1">
						{run.findings.map((f) => (
							<li key={f.id} className="flex items-start gap-2 text-[11px]">
								<span
									className={cn(
										"mt-0.5 font-mono",
										f.severity === "error" ? "text-error" : f.severity === "warning" ? "text-warning" : "text-passive",
									)}
								>
									{f.severity}
								</span>
								<span className="min-w-0 flex-1">
									<span className="font-mono text-passive">
										{f.path}:{f.line}
									</span>{" "}
									<span className="text-muted-foreground">{f.body}</span>
								</span>
							</li>
						))}
					</ul>
				)}
			</CardContent>
		</Card>
	);
}
