import { useState } from "react";
import { Button } from "./ui/button";
import { useImportStatus, useRunImport } from "../hooks/useImportStatus";

// ImportOffer surfaces the first-run legacy-AO import opt-in on the dashboard.
// `ao start` is headless and never prompts; the daemon reports availability via
// GET /api/v1/import, and this banner is where the user accepts or declines.
//
// Accept runs the import through the live daemon (POST /api/v1/import); on
// success the workspace query is invalidated so the imported projects and the
// revived orchestrator appear, and the offer retires (the DB is no longer
// empty). Decline dismisses for the session; the data is untouched and the
// user can run `ao import` or restart later.
export function ImportOffer() {
	const status = useImportStatus();
	const runImport = useRunImport();
	const [dismissed, setDismissed] = useState(false);

	const available = status.data?.available ?? false;
	if (!available || dismissed || runImport.isSuccess) return null;

	const legacyRoot = status.data?.legacyRoot ?? "your earlier AO";
	const error = runImport.error?.message;

	return (
		<div className="mx-[18px] mt-[18px] flex flex-col gap-3 rounded-[11px] border border-primary/40 bg-surface p-4">
			<div className="flex items-start gap-3">
				<div className="min-w-0 flex-1">
					<p className="text-[13px] font-medium text-foreground">
						Import projects and orchestrator from your earlier AO?
					</p>
					<p className="mt-1 text-[12px] leading-[1.5] text-muted-foreground">
						We found an existing install at <span className="font-mono text-[11px] text-passive">{legacyRoot}</span>.
						Importing brings in your projects and revives the orchestrator. Your old files are never modified, and you
						can do this later instead.
					</p>
					{error && <p className="mt-2 text-[12px] text-error">Import failed: {error}</p>}
				</div>
				<div className="flex shrink-0 items-center gap-2">
					<Button
						variant="ghost"
						size="sm"
						disabled={runImport.isPending}
						onClick={() => setDismissed(true)}
						type="button"
					>
						Not now
					</Button>
					<Button
						variant="primary"
						size="sm"
						disabled={runImport.isPending}
						onClick={() => runImport.mutate()}
						type="button"
					>
						{runImport.isPending ? "Importing…" : "Import"}
					</Button>
				</div>
			</div>
		</div>
	);
}
