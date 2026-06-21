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
				className="max-w-full truncate text-accent hover:underline"
				href={link.href}
				onClick={(event) => event.stopPropagation()}
				rel="noopener noreferrer"
				target="_blank"
				title={link.title}
			>
				{link.label}
			</a>
		);
	}
	return (
		<span className="max-w-full truncate text-muted-foreground" title={link.title}>
			{link.label}
		</span>
	);
}
