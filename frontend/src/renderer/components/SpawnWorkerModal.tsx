import * as Dialog from "@radix-ui/react-dialog";
import { ChevronDown, CornerDownLeft, X } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";
import type { AgentProvider, WorkspaceSummary } from "../types/workspace";
import { Button } from "./ui/button";
import { cn } from "../lib/utils";

const agentOptions: { value: AgentProvider; label: string }[] = [
	{ value: "claude-code", label: "claude-code" },
	{ value: "codex", label: "codex" },
	{ value: "opencode", label: "opencode" },
	{ value: "amp", label: "amp" },
	{ value: "goose", label: "goose" },
	{ value: "kiro", label: "kiro" },
	{ value: "kimi", label: "kimi" },
	{ value: "crush", label: "crush" },
	{ value: "vibe", label: "vibe" },
];

const basedOnTabs = ["Branch", "Issue", "Pull Request"] as const;
type BasedOn = (typeof basedOnTabs)[number];

const NAME_RULE = /^[a-z0-9-]+$/;

type SpawnWorkerModalProps = {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	workspaces: WorkspaceSummary[];
	defaultProjectId?: string;
	onCreateTask: (input: { projectId: string; prompt: string; name?: string; harness?: AgentProvider }) => Promise<void>;
};

export function SpawnWorkerModal({
	open,
	onOpenChange,
	workspaces,
	defaultProjectId,
	onCreateTask,
}: SpawnWorkerModalProps) {
	const fallbackProjectId = defaultProjectId ?? workspaces[0]?.id ?? "";
	const [name, setName] = useState("");
	const [projectId, setProjectId] = useState(fallbackProjectId);
	const [agent, setAgent] = useState<AgentProvider>("claude-code");
	const [basedOn, setBasedOn] = useState<BasedOn>("Branch");
	const [tab, setTab] = useState<"Prompt" | "Workspace">("Prompt");
	const [prompt, setPrompt] = useState("");
	const [error, setError] = useState<string | null>(null);
	const [isSubmitting, setIsSubmitting] = useState(false);

	// Reset to the launching project each time the dialog opens.
	useEffect(() => {
		if (open) {
			setProjectId(fallbackProjectId);
			setError(null);
		}
	}, [open, fallbackProjectId]);

	const selectedWorkspace = workspaces.find((workspace) => workspace.id === projectId) ?? workspaces[0];
	const nameValid = name === "" || NAME_RULE.test(name);
	const canSubmit = prompt.trim().length > 0 && projectId !== "" && nameValid && !isSubmitting;

	const submit = async (event?: FormEvent<HTMLFormElement>) => {
		event?.preventDefault();
		if (!canSubmit) return;
		setError(null);
		setIsSubmitting(true);
		try {
			await onCreateTask({
				projectId,
				prompt: prompt.trim(),
				name: name || undefined,
				harness: agent,
			});
			setName("");
			setPrompt("");
			onOpenChange(false);
		} catch (err) {
			setError(err instanceof Error ? err.message : "Could not spawn worker");
		} finally {
			setIsSubmitting(false);
		}
	};

	return (
		<Dialog.Root open={open} onOpenChange={onOpenChange}>
			<Dialog.Portal>
				<Dialog.Overlay className="fixed inset-0 z-40 bg-black/50 data-[state=open]:animate-overlay-in" />
				<Dialog.Content
					aria-describedby={undefined}
					className="fixed left-1/2 top-1/2 z-50 w-[512px] max-w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 overflow-hidden rounded-xl bg-background text-foreground shadow-[0_24px_70px_rgb(0_0_0_/_0.55)] ring-1 ring-foreground/10 focus-visible:outline-none data-[state=open]:animate-modal-in"
					onKeyDown={(event) => {
						if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
							event.preventDefault();
							void submit();
						}
					}}
				>
					<div className="flex items-center gap-2 border-b border-border px-[18px] py-[15px]">
						<Dialog.Title className="font-mono text-[11px] uppercase tracking-[0.14em] text-passive">
							New worker
						</Dialog.Title>
						<Dialog.Close
							aria-label="Close"
							className="ml-auto inline-flex text-passive transition-colors hover:text-foreground"
						>
							<X className="h-[15px] w-[15px]" aria-hidden="true" />
						</Dialog.Close>
					</div>

					<form className="flex flex-col gap-[15px] p-[18px]" onSubmit={submit}>
						<div className="flex flex-col gap-1.5">
							<input
								aria-label="Worker name"
								autoFocus
								className="w-full border-none bg-transparent p-0 text-lg font-medium text-foreground outline-none placeholder:text-passive"
								onChange={(event) => setName(event.target.value)}
								placeholder="worker-name"
								value={name}
							/>
							<span className={cn("text-[11.5px]", nameValid ? "text-passive" : "text-error")}>
								Worker names allow letters, numbers, and hyphens.
							</span>
						</div>

						<SelectRow
							label="Project"
							aria-label="Project"
							onChange={setProjectId}
							value={projectId}
							options={workspaces.map((workspace) => ({ value: workspace.id, label: workspace.name }))}
						/>
						<SelectRow
							label="Agent"
							aria-label="Agent"
							onChange={(value) => setAgent(value as AgentProvider)}
							value={agent}
							options={agentOptions}
						/>

						<div className="overflow-hidden rounded-lg border border-border">
							<div className="flex items-center gap-2 border-b border-border px-2.5 py-1.5">
								<span className="text-[13px] text-muted-foreground">Based on</span>
								<div className="ml-auto inline-flex overflow-hidden rounded-md border border-border">
									{basedOnTabs.map((option) => (
										<button
											className={cn(
												"px-2.5 py-0.5 text-[11.5px] transition-colors",
												basedOn === option
													? "bg-raised text-foreground"
													: "text-muted-foreground hover:text-foreground",
											)}
											key={option}
											onClick={() => setBasedOn(option)}
											type="button"
										>
											{option}
										</button>
									))}
								</div>
							</div>
							<div className="p-2.5">
								<p className="px-1 py-1.5 text-[12.5px] text-passive">
									{/* The API has no per-spawn base branch — the worker branches off the
                      project's configured default branch in a fresh worktree. */}
									{basedOn === "Branch"
										? "Branches off the project's default branch in a fresh worktree."
										: basedOn === "Issue"
											? "Pick an issue to start from."
											: "Pick a pull request to start from."}
								</p>
							</div>
						</div>

						<div className="flex flex-col gap-2">
							<div className="flex gap-1">
								{(["Prompt", "Workspace"] as const).map((option) => (
									<button
										className={cn(
											"rounded-md px-2.5 py-1 text-[12.5px] transition-colors",
											tab === option ? "bg-raised text-foreground" : "text-muted-foreground hover:text-foreground",
										)}
										key={option}
										onClick={() => setTab(option)}
										type="button"
									>
										{option}
									</button>
								))}
							</div>
							{tab === "Prompt" ? (
								<textarea
									aria-label="Prompt"
									className="min-h-[74px] w-full resize-none rounded-md border border-border bg-transparent px-2.5 py-2 text-[13px] text-foreground outline-none placeholder:text-passive focus-visible:border-accent focus-visible:ring-2 focus-visible:ring-accent-weak"
									onChange={(event) => setPrompt(event.target.value)}
									placeholder="What should this worker do?"
									value={prompt}
								/>
							) : (
								<p className="rounded-md border border-border px-2.5 py-2 font-mono text-[12px] text-passive">
									~/.rc/wt/{selectedWorkspace?.name ?? "project"}/{name || "worker-name"}
								</p>
							)}
						</div>

						{error && (
							<p className="text-[12px] text-error" role="alert">
								{error}
							</p>
						)}

						<div className="-mx-[18px] -mb-[18px] flex justify-end border-t border-border bg-surface px-3.5 py-3">
							<Button disabled={!canSubmit} type="submit" variant="primary">
								Spawn worker
								<span className="ml-0.5 inline-flex items-center gap-0.5 rounded border border-accent-foreground/35 px-1 font-mono text-[10px] opacity-70">
									<CornerDownLeft className="h-2.5 w-2.5" aria-hidden="true" />
								</span>
							</Button>
						</div>
					</form>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}

function SelectRow({
	label,
	"aria-label": ariaLabel,
	value,
	options,
	onChange,
}: {
	label: string;
	"aria-label": string;
	value: string;
	options: { value: string; label: string }[];
	onChange: (value: string) => void;
}) {
	return (
		<div className="flex items-center gap-2.5">
			<span className="text-[13px] text-muted-foreground">{label}</span>
			<div className="ml-auto">
				<SelectControl aria-label={ariaLabel} onChange={onChange} options={options} value={value} />
			</div>
		</div>
	);
}

function SelectControl({
	"aria-label": ariaLabel,
	value,
	options,
	onChange,
	className,
}: {
	"aria-label": string;
	value: string;
	options: { value: string; label: string }[];
	onChange: (value: string) => void;
	className?: string;
}) {
	return (
		<div className={cn("relative inline-flex h-7 items-center rounded-md border border-border", className)}>
			<select
				aria-label={ariaLabel}
				className="h-full w-full cursor-pointer appearance-none bg-transparent pl-2.5 pr-7 text-[13px] text-foreground outline-none"
				onChange={(event) => onChange(event.target.value)}
				value={value}
			>
				{options.map((option) => (
					<option key={option.value} value={option.value}>
						{option.label}
					</option>
				))}
			</select>
			<ChevronDown className="pointer-events-none absolute right-2 h-[13px] w-[13px] text-passive" aria-hidden="true" />
		</div>
	);
}
