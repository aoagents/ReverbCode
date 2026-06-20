import { useEffect, useState } from "react";
import { AGENT_OPTIONS } from "../lib/agent-options";
import { Button } from "./ui/button";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "./ui/sheet";

export type CreateProjectAgentSelection = {
	workerAgent: string;
	orchestratorAgent: string;
};

type CreateProjectAgentSheetProps = {
	error?: string | null;
	isCreating: boolean;
	onOpenChange: (open: boolean) => void;
	onSubmit: (selection: CreateProjectAgentSelection) => Promise<void>;
	open: boolean;
	path: string | null;
};

export function CreateProjectAgentSheet({
	error,
	isCreating,
	onOpenChange,
	onSubmit,
	open,
	path,
}: CreateProjectAgentSheetProps) {
	const [workerAgent, setWorkerAgent] = useState("");
	const [orchestratorAgent, setOrchestratorAgent] = useState("");
	const canSubmit = workerAgent !== "" && orchestratorAgent !== "" && !isCreating;

	useEffect(() => {
		if (!open) {
			setWorkerAgent("");
			setOrchestratorAgent("");
		}
	}, [open, path]);

	return (
		<Sheet open={open} onOpenChange={(next) => !isCreating && onOpenChange(next)}>
			<SheetContent side="right" className="w-[360px] sm:max-w-[360px]">
				<SheetHeader>
					<SheetTitle className="text-[15px]">Project agents</SheetTitle>
					<SheetDescription className="break-all text-[12px]">{path ?? ""}</SheetDescription>
				</SheetHeader>
				<form
					className="flex min-h-0 flex-1 flex-col"
					onSubmit={(event) => {
						event.preventDefault();
						if (!canSubmit) return;
						void onSubmit({ workerAgent, orchestratorAgent });
					}}
				>
					<div className="flex flex-1 flex-col gap-4 px-4">
						<RequiredAgentField
							id="newProjectWorkerAgent"
							label="Worker agent"
							placeholder="Select worker agent"
							value={workerAgent}
							onChange={setWorkerAgent}
						/>
						<RequiredAgentField
							id="newProjectOrchestratorAgent"
							label="Orchestrator agent"
							placeholder="Select orchestrator agent"
							value={orchestratorAgent}
							onChange={setOrchestratorAgent}
						/>
						{error && <p className="text-[12px] leading-5 text-error">{error}</p>}
					</div>
					<SheetFooter className="flex-row justify-end">
						<Button type="button" variant="outline" disabled={isCreating} onClick={() => onOpenChange(false)}>
							Cancel
						</Button>
						<Button type="submit" variant="primary" disabled={!canSubmit}>
							{isCreating ? "Creating..." : "Create and start"}
						</Button>
					</SheetFooter>
				</form>
			</SheetContent>
		</Sheet>
	);
}

export function RequiredAgentField({
	id,
	label,
	onChange,
	placeholder,
	value,
}: {
	id: string;
	label: string;
	onChange: (value: string) => void;
	placeholder: string;
	value: string;
}) {
	return (
		<div className="flex flex-col gap-1.5">
			<Label htmlFor={id} className="text-[12px] text-muted-foreground">
				{label}
			</Label>
			<Select value={value} onValueChange={onChange}>
				<SelectTrigger id={id} className="h-8 w-full text-[13px]" aria-invalid={value === ""}>
					<SelectValue placeholder={placeholder} />
				</SelectTrigger>
				<SelectContent>
					{AGENT_OPTIONS.map((agent) => (
						<SelectItem key={agent} value={agent}>
							{agent}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
		</div>
	);
}
