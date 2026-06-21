import { ArrowUpDown, ArrowUpRight } from "lucide-react";
import { Fragment, type ReactNode } from "react";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { prAttentionItems, prStatusRows, type PRAttentionLink, type PRDisplayTone } from "../lib/pr-display";
import { cn } from "../lib/utils";

const toneClass: Record<PRDisplayTone, string> = {
	neutral: "text-muted-foreground",
	passive: "text-passive",
	success: "text-success",
	warning: "text-warning",
	error: "text-error",
};

export function PRStatusStrip({ className, pr }: { className?: string; pr: SessionPRSummary }) {
	return (
		<div className={cn("flex flex-wrap gap-x-3 gap-y-1 font-mono text-[10.5px]", className)}>
			{prStatusRows(pr).map((row) => (
				<span key={row.key} className="min-w-0">
					<span className="text-passive">{row.label}</span>{" "}
					<span className={cn("font-medium", toneClass[row.tone])}>{row.value}</span>
					{row.detail ? <span className="text-passive"> · {row.detail}</span> : null}
				</span>
			))}
		</div>
	);
}

export function PRSummaryMeta({
	className,
	leading,
	pr,
}: {
	className?: string;
	leading?: string;
	pr: SessionPRSummary;
}) {
	const branchRange = prBranchRange(pr);
	const hasDiff = hasDiffMetadata(pr);
	const primary = [leading, branchRange, pr.author].filter(Boolean);
	if (primary.length === 0 && !hasDiff) {
		return null;
	}
	return (
		<div className={cn("min-w-0 font-mono text-[10.5px] leading-4", className)}>
			{primary.length > 0 ? <div className="truncate text-passive">{primary.join(" · ")}</div> : null}
			{hasDiff ? <PRDiffMeta pr={pr} /> : null}
		</div>
	);
}

function PRDiffMeta({ pr }: { pr: SessionPRSummary }) {
	const parts: ReactNode[] = [];
	if (pr.changedFiles > 0) {
		parts.push(
			<span className="inline-flex items-center gap-0.5 text-warning" key="files">
				<ArrowUpDown aria-hidden="true" className="h-2.5 w-2.5 shrink-0" strokeWidth={2.2} />
				{pr.changedFiles} {pluralize("file", pr.changedFiles)}
			</span>,
		);
	}
	if (pr.additions > 0) {
		parts.push(
			<span className="text-success" key="additions">
				+{pr.additions}
			</span>,
		);
	}
	if (pr.deletions > 0) {
		parts.push(
			<span className="text-error" key="deletions">
				-{pr.deletions}
			</span>,
		);
	}
	return (
		<div className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-muted-foreground">
			{parts.map((part, index) => (
				<Fragment key={index}>
					{index > 0 ? <span className="text-passive">·</span> : null}
					{part}
				</Fragment>
			))}
		</div>
	);
}

export function PRAttentionPanel({
	className,
	interactiveLinks = true,
	maxItems = 3,
	pr,
}: {
	className?: string;
	interactiveLinks?: boolean;
	maxItems?: number;
	pr: SessionPRSummary;
}) {
	const items = prAttentionItems(pr);
	if (items.length === 0) {
		return null;
	}
	const visible = items.slice(0, maxItems);
	const extra = items.length - visible.length;
	return (
		<div className={cn("mt-2 border-t border-border pt-2", className)}>
			<div className="mb-1 font-mono text-[9.5px] font-semibold uppercase tracking-[0.08em] text-passive">
				Needs attention
			</div>
			<div className="flex flex-col gap-1.5">
				{visible.map((item) => (
					<div key={item.kind} className="min-w-0 text-[11px] leading-4">
						<div className={cn("font-medium", toneClass[item.tone])}>{item.title}</div>
						{item.summary ? (
							<div className="truncate font-mono text-[10.5px] text-muted-foreground">{item.summary}</div>
						) : null}
						{item.links.length > 0 ? (
							<div className="mt-0.5 flex min-w-0 flex-wrap gap-x-1.5 gap-y-1 font-mono text-[10.5px]">
								{item.links.map((link, index) => (
									<AttentionLink
										interactive={interactiveLinks}
										key={`${item.kind}-${index}-${link.label}`}
										link={link}
									/>
								))}
								{item.overflowLabel ? <span className="text-passive">{item.overflowLabel}</span> : null}
							</div>
						) : null}
					</div>
				))}
				{extra > 0 ? <div className="font-mono text-[10.5px] text-passive">+{extra} more</div> : null}
			</div>
		</div>
	);
}

function AttentionLink({ interactive, link }: { interactive: boolean; link: PRAttentionLink }) {
	if (interactive && link.href) {
		return (
			<a
				className="inline-flex max-w-full min-w-0 items-center gap-0.5 text-accent hover:underline"
				href={link.href}
				onClick={(event) => event.stopPropagation()}
				rel="noopener noreferrer"
				target="_blank"
				title={link.title}
			>
				<span className="truncate">{link.label}</span>
				<ArrowUpRight aria-hidden="true" className="h-2.5 w-2.5 shrink-0" strokeWidth={2} />
			</a>
		);
	}
	return (
		<span className="max-w-full truncate text-muted-foreground" title={link.title}>
			{link.label}
		</span>
	);
}

function prBranchRange(pr: SessionPRSummary): string | undefined {
	if (pr.sourceBranch && pr.targetBranch) {
		return `${pr.sourceBranch} -> ${pr.targetBranch}`;
	}
	if (pr.sourceBranch) {
		return pr.sourceBranch;
	}
	if (pr.targetBranch) {
		return `-> ${pr.targetBranch}`;
	}
	return undefined;
}

function hasDiffMetadata(pr: SessionPRSummary): boolean {
	return pr.changedFiles > 0 || pr.additions > 0 || pr.deletions > 0;
}

function pluralize(noun: string, count: number): string {
	return count === 1 ? noun : `${noun}s`;
}
