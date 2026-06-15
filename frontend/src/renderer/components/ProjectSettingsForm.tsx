import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { DashboardSubhead } from "./DashboardSubhead";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

type Project = components["schemas"]["Project"];
type ProjectConfig = components["schemas"]["ProjectConfig"];
type AgentInfo = components["schemas"]["AgentInfo"];
type AgentCatalog = components["schemas"]["ListAgentsResponse"];
type Session = components["schemas"]["Session"];

const PERMISSION_MODE_OPTIONS = [
	{ value: "default", label: "Default" },
	{ value: "accept-edits", label: "Accept edits" },
	{ value: "auto", label: "Auto" },
	{ value: "bypass-permissions", label: "Bypass permissions" },
] as const;

const projectQueryKey = (id: string) => ["project", id] as const;

export function ProjectSettingsForm({ projectId }: { projectId: string }) {
	const queryClient = useQueryClient();

	const query = useQuery({
		queryKey: projectQueryKey(projectId),
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			if (data?.status !== "ok") throw new Error("Project config is unavailable (degraded).");
			return data.project as Project;
		},
	});
	const agentsQuery = useQuery({
		queryKey: ["agents"],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/agents");
			if (error) throw new Error(apiErrorMessage(error));
			return data as AgentCatalog;
		},
	});

	if (query.isLoading || agentsQuery.isLoading) {
		return <CenteredNote>Loading project settings…</CenteredNote>;
	}
	if (query.isError || !query.data) {
		return (
			<CenteredNote>{query.error instanceof Error ? query.error.message : "Could not load project."}</CenteredNote>
		);
	}
	if (agentsQuery.isError || !agentsQuery.data) {
		return (
			<CenteredNote>
				{agentsQuery.error instanceof Error ? agentsQuery.error.message : "Could not load agent catalog."}
			</CenteredNote>
		);
	}

	return (
		<div className="flex h-full min-h-0 flex-col bg-background text-foreground">
			<DashboardSubhead title="Settings" subtitle={query.data.path} />
			<div className="min-h-0 flex-1 overflow-y-auto p-[18px]">
				<SettingsBody
					key={projectId}
					project={query.data}
					agents={agentsQuery.data}
					onSaved={() => queryClient.invalidateQueries({ queryKey: workspaceQueryKey })}
					projectId={projectId}
				/>
			</div>
		</div>
	);
}

function SettingsBody({
	project,
	projectId,
	agents,
	onSaved,
}: {
	project: Project;
	projectId: string;
	agents: AgentCatalog;
	onSaved: () => void;
}) {
	const queryClient = useQueryClient();
	const config = project.config ?? {};
	const agentOptions = agents.installed ?? [];
	const supportedAgents = agents.supported ?? [];
	const hasInstalledAgents = agentOptions.length > 0;
	const [form, setForm] = useState({
		defaultBranch: config.defaultBranch ?? project.defaultBranch ?? "",
		sessionPrefix: config.sessionPrefix ?? "",
		workerAgent: config.worker?.agent ?? "",
		orchestratorAgent: config.orchestrator?.agent ?? "",
		model: config.agentConfig?.model ?? "",
		permissions: config.agentConfig?.permissions ?? "",
	});
	const [savedAt, setSavedAt] = useState<number | null>(null);
	const [restartedAt, setRestartedAt] = useState<number | null>(null);

	const mutation = useMutation({
		mutationFn: async () => {
			const orchestratorAgentChanged = (config.orchestrator?.agent ?? "") !== form.orchestratorAgent;
			if (orchestratorAgentChanged) {
				await assertOrchestratorCanRestart(projectId);
			}
			// PUT replaces the whole config; merge the edited fields over what loaded
			// so we don't drop env/symlinks/postCreate the form doesn't expose.
			const next: ProjectConfig = {
				...config,
				defaultBranch: form.defaultBranch || undefined,
				sessionPrefix: form.sessionPrefix || undefined,
				worker: blankToUndefined({ ...config.worker, agent: form.workerAgent || undefined }),
				orchestrator: blankToUndefined({ ...config.orchestrator, agent: form.orchestratorAgent || undefined }),
				agentConfig: blankToUndefined({
					...config.agentConfig,
					model: form.model || undefined,
					permissions: form.permissions || undefined,
				}),
			};
			const { error } = await apiClient.PUT("/api/v1/projects/{id}/config", {
				params: { path: { id: projectId } },
				body: { config: next },
			});
			if (error) throw new Error(apiErrorMessage(error));
			if (orchestratorAgentChanged) {
				const { error: restartError } = await apiClient.POST("/api/v1/orchestrators", {
					body: { projectId, clean: true },
				});
				if (restartError) throw new Error(`Saved config, but failed to restart orchestrator: ${apiErrorMessage(restartError)}`);
			}
			return { restarted: orchestratorAgentChanged };
		},
		onSuccess: ({ restarted }) => {
			setSavedAt(Date.now());
			setRestartedAt(restarted ? Date.now() : null);
			void queryClient.invalidateQueries({ queryKey: ["project", projectId] });
			onSaved();
		},
	});

	return (
		<form
			className="mx-auto flex max-w-2xl flex-col gap-4"
			onSubmit={(event) => {
				event.preventDefault();
				mutation.mutate();
			}}
		>
			<Card>
				<CardHeader>
					<CardTitle className="text-[13px]">Identity</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-2 font-mono text-[12px] text-muted-foreground">
					<ReadonlyRow label="id" value={project.id} />
					<ReadonlyRow label="path" value={project.path} />
					<ReadonlyRow label="repo" value={project.repo || "—"} />
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle className="text-[13px]">Worktrees</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-4">
					<Field label="Default branch" htmlFor="defaultBranch">
						<input
							id="defaultBranch"
							className="h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
							value={form.defaultBranch}
							onChange={(e) => setForm((f) => ({ ...f, defaultBranch: e.target.value }))}
							placeholder="main"
						/>
					</Field>
					<Field label="Session prefix" htmlFor="sessionPrefix">
						<input
							id="sessionPrefix"
							className="h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
							value={form.sessionPrefix}
							onChange={(e) => setForm((f) => ({ ...f, sessionPrefix: e.target.value }))}
							placeholder="ao"
						/>
					</Field>
				</CardContent>
			</Card>

			<Card>
				<CardHeader>
					<CardTitle className="text-[13px]">Agents</CardTitle>
				</CardHeader>
				<CardContent className="flex flex-col gap-4">
					<div className="rounded-md border border-border bg-muted/20 px-3 py-2 text-[12px] leading-5 text-muted-foreground">
						<div>
							{agents.counts.installed} of {agents.counts.supported} supported agents installed on this machine.
						</div>
						<div>Only installed agents are selectable. Orchestrator agent changes restart the orchestrator.</div>
						{!hasInstalledAgents && (
							<div className="mt-1 text-warning">No supported agent runtime was detected.</div>
						)}
					</div>
					<Field label="Default worker agent" htmlFor="workerAgent">
						<AgentSelect
							id="workerAgent"
							value={form.workerAgent}
							installed={agentOptions}
							supported={supportedAgents}
							disabled={!hasInstalledAgents}
							onChange={(v) => setForm((f) => ({ ...f, workerAgent: v }))}
						/>
					</Field>
					<Field label="Default orchestrator agent" htmlFor="orchestratorAgent">
						<AgentSelect
							id="orchestratorAgent"
							value={form.orchestratorAgent}
							installed={agentOptions}
							supported={supportedAgents}
							disabled={!hasInstalledAgents}
							onChange={(v) => setForm((f) => ({ ...f, orchestratorAgent: v }))}
						/>
					</Field>
					<Field label="Model override" htmlFor="model">
						<input
							id="model"
							className="h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
							value={form.model}
							onChange={(e) => setForm((f) => ({ ...f, model: e.target.value }))}
							placeholder="(agent default)"
						/>
					</Field>
					<Field label="Permission mode" htmlFor="permissionMode">
						<PermissionModeSelect
							id="permissionMode"
							value={form.permissions}
							onChange={(v) => setForm((f) => ({ ...f, permissions: v }))}
						/>
					</Field>
				</CardContent>
			</Card>

			<div className="flex items-center gap-3">
				<Button type="submit" variant="primary" disabled={mutation.isPending}>
					{mutation.isPending ? "Saving…" : "Save changes"}
				</Button>
				{mutation.isError && (
					<span className="text-[12px] text-error">
						{mutation.error instanceof Error ? mutation.error.message : "Save failed"}
					</span>
				)}
				{savedAt && !mutation.isPending && !mutation.isError && (
					<span className="text-[12px] text-success">
						{restartedAt ? "Saved. Orchestrator restarted." : "Saved."}
					</span>
				)}
			</div>
		</form>
	);
}

async function assertOrchestratorCanRestart(projectId: string) {
	const { data, error } = await apiClient.GET("/api/v1/orchestrators");
	if (error) throw new Error(`Could not check orchestrator state: ${apiErrorMessage(error)}`);
	const busy = (data?.sessions ?? []).find(
		(session) =>
			session.projectId === projectId &&
			session.kind === "orchestrator" &&
			!session.isTerminated &&
			orchestratorRestartBlocked(session),
	);
	if (busy) {
		throw new Error("Orchestrator is currently active. Wait until it is idle before switching agents.");
	}
}

function orchestratorRestartBlocked(session: Session) {
	if (session.status === "idle" || session.status === "terminated") return false;
	return true;
}

function PermissionModeSelect({
	id,
	value,
	onChange,
}: {
	id: string;
	value: string;
	onChange: (value: string) => void;
}) {
	return (
		<Select value={value || "__default__"} onValueChange={(v) => onChange(v === "__default__" ? "" : v)}>
			<SelectTrigger id={id} className="h-8 w-full text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value="__default__">Project default</SelectItem>
				{PERMISSION_MODE_OPTIONS.map((opt) => (
					<SelectItem key={opt.value} value={opt.value}>
						{opt.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

function AgentSelect({
	id,
	value,
	installed,
	supported,
	disabled,
	onChange,
}: {
	id: string;
	value: string;
	installed: AgentInfo[];
	supported: AgentInfo[];
	disabled?: boolean;
	onChange: (value: string) => void;
}) {
	// "" sentinel → daemon default; Select can't hold an empty value, so map it.
	const installedIds = new Set(installed.map((agent) => agent.id));
	const supportedById = new Map(supported.map((agent) => [agent.id, agent]));
	const missingCurrent = value !== "" && !installedIds.has(value);
	const current = supportedById.get(value);
	return (
		<div className="flex flex-col gap-1.5">
			<Select
				value={value || "__default__"}
				disabled={disabled}
				onValueChange={(v) => onChange(v === "__default__" ? "" : v)}
			>
				<SelectTrigger id={id} className="h-8 w-full text-[13px]">
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					<SelectItem value="__default__">Daemon default</SelectItem>
					{missingCurrent && (
						<SelectItem value={value}>
							{current?.label ?? value} <span className="text-warning">(Not detected)</span>
						</SelectItem>
					)}
					{installed.map((agent) => (
						<SelectItem key={agent.id} value={agent.id}>
							{agent.label}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
			{missingCurrent && (
				<span className="text-[12px] text-warning">
					{current?.label ?? value} is configured but was not detected on this machine.
				</span>
			)}
		</div>
	);
}

function Field({ label, htmlFor, children }: { label: string; htmlFor?: string; children: React.ReactNode }) {
	return (
		<div className="flex flex-col gap-1.5">
			<Label htmlFor={htmlFor} className="text-[12px] text-muted-foreground">
				{label}
			</Label>
			{children}
		</div>
	);
}

function ReadonlyRow({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex items-center gap-3">
			<span className="w-12 shrink-0 text-passive">{label}</span>
			<span className="min-w-0 flex-1 truncate text-foreground">{value}</span>
		</div>
	);
}

function CenteredNote({ children }: { children: React.ReactNode }) {
	return (
		<div className="grid h-full place-items-center bg-background p-6 text-center text-[12px] text-passive">
			{children}
		</div>
	);
}

// Drop an object whose every value is undefined so we send `undefined` (omit)
// rather than an empty {} the daemon would persist.
function blankToUndefined<T extends object>(obj: T): T | undefined {
	return Object.values(obj).some((v) => v !== undefined) ? obj : undefined;
}
