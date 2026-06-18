import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, X } from "lucide-react";
import type { ReactNode } from "react";
import { useEffect, useMemo, useState } from "react";
import { agentDefaultsQueryKey, fetchAgentDefaults, saveAgentDefaults } from "../lib/agent-defaults";
import { AGENT_OPTIONS, type AgentOption } from "../lib/agent-options";
import { Button } from "./ui/button";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

const AGENT_SELECT_PLACEHOLDER = "__select_agent__";

type AgentDefaultsDialogProps = {
	daemonReady: boolean;
	open: boolean;
	onOpenChange: (open: boolean) => void;
};

export function AgentDefaultsDialog({ daemonReady, open, onOpenChange }: AgentDefaultsDialogProps) {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: agentDefaultsQueryKey,
		queryFn: fetchAgentDefaults,
		enabled: daemonReady,
	});
	const firstRunRequired =
		daemonReady && (query.isLoading || query.isError || (query.isSuccess && !query.data.configured));
	const visible = open || firstRunRequired;
	const locked = firstRunRequired;
	const [workerAgent, setWorkerAgent] = useState("");
	const [orchestratorAgent, setOrchestratorAgent] = useState("");

	useEffect(() => {
		if (!visible || !query.data) return;
		setWorkerAgent(query.data.defaultWorkerAgent ?? "");
		setOrchestratorAgent(query.data.defaultOrchestratorAgent ?? "");
	}, [query.data, visible]);

	const canSave = daemonReady && workerAgent !== "" && orchestratorAgent !== "";
	const title = firstRunRequired ? "Choose Default Agents" : "Default Agents";
	const mutation = useMutation({
		mutationFn: () =>
			saveAgentDefaults({
				defaultWorkerAgent: workerAgent as AgentOption,
				defaultOrchestratorAgent: orchestratorAgent as AgentOption,
			}),
		onSuccess: (defaults) => {
			queryClient.setQueryData(agentDefaultsQueryKey, defaults);
			onOpenChange(false);
		},
	});

	const statusText = useMemo(() => {
		if (query.isLoading) return "Loading agent settings...";
		if (!daemonReady) return "Daemon is not ready.";
		if (query.isError) return query.error instanceof Error ? query.error.message : "Could not load agent settings";
		if (mutation.isError)
			return mutation.error instanceof Error ? mutation.error.message : "Could not save agent settings";
		return null;
	}, [daemonReady, mutation.error, mutation.isError, query.error, query.isError, query.isLoading]);

	if (!visible) return null;

	const close = () => {
		if (!locked) onOpenChange(false);
	};

	return (
		<div className="fixed inset-0 z-[80] grid place-items-center bg-black/45 px-4" role="presentation">
			<form
				aria-labelledby="agent-defaults-title"
				className="flex w-full max-w-[420px] flex-col gap-5 rounded-lg border border-border bg-surface p-5 text-foreground shadow-[var(--shadow)]"
				onSubmit={(event) => {
					event.preventDefault();
					if (canSave) mutation.mutate();
				}}
				role="dialog"
				aria-modal="true"
			>
				<div className="flex items-start gap-3">
					<div className="grid size-8 shrink-0 place-items-center rounded-md border border-border bg-raised text-accent">
						<Bot className="size-4" aria-hidden="true" />
					</div>
					<div className="min-w-0 flex-1">
						<h2 id="agent-defaults-title" className="text-[14px] font-semibold text-foreground">
							{title}
						</h2>
						<p className="mt-1 text-[12px] text-passive">
							{firstRunRequired ? "Required before spawning sessions." : "Used when a project has no role override."}
						</p>
					</div>
					{!locked && (
						<button
							aria-label="Close"
							className="grid size-7 shrink-0 place-items-center rounded-md text-passive transition-colors hover:bg-interactive-hover hover:text-foreground"
							onClick={close}
							type="button"
						>
							<X className="size-4" aria-hidden="true" />
						</button>
					)}
				</div>

				<div className="flex flex-col gap-4">
					<Field label="Worker agent" htmlFor="defaultWorkerAgent">
						<AgentSelect id="defaultWorkerAgent" value={workerAgent} onChange={setWorkerAgent} />
					</Field>
					<Field label="Orchestrator agent" htmlFor="defaultOrchestratorAgent">
						<AgentSelect id="defaultOrchestratorAgent" value={orchestratorAgent} onChange={setOrchestratorAgent} />
					</Field>
				</div>

				<div className="flex min-h-8 items-center justify-between gap-3">
					<span className="min-w-0 flex-1 text-[12px] text-error" role={statusText ? "status" : undefined}>
						{statusText}
					</span>
					<Button disabled={!canSave || mutation.isPending || query.isLoading} type="submit" variant="primary">
						{mutation.isPending ? "Saving..." : "Save defaults"}
					</Button>
				</div>
			</form>
		</div>
	);
}

function AgentSelect({ id, value, onChange }: { id: string; value: string; onChange: (value: string) => void }) {
	return (
		<Select
			value={value || AGENT_SELECT_PLACEHOLDER}
			onValueChange={(next) => {
				if (next !== AGENT_SELECT_PLACEHOLDER) onChange(next);
			}}
		>
			<SelectTrigger id={id} className="h-8 w-full text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value={AGENT_SELECT_PLACEHOLDER} disabled>
					Select agent
				</SelectItem>
				{AGENT_OPTIONS.map((agent) => (
					<SelectItem key={agent} value={agent}>
						{agent}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

function Field({ label, htmlFor, children }: { label: string; htmlFor: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-1.5">
			<Label htmlFor={htmlFor} className="text-[12px] text-muted-foreground">
				{label}
			</Label>
			{children}
		</div>
	);
}
