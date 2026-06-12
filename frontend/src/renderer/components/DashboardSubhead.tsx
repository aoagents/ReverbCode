// The board subhead (mc-board .dashboard-main__subhead): a 21px bold title with
// a muted one-line subtitle, optionally a trailing count.
export function DashboardSubhead({ title, subtitle, count }: { title: string; subtitle: string; count?: number }) {
	return (
		<div className="flex items-baseline gap-3 px-[18px] pt-[22px]">
			<h1 className="text-[21px] font-bold tracking-[-0.025em] text-foreground">{title}</h1>
			{typeof count === "number" && <span className="font-mono text-[13px] text-passive">{count}</span>}
			<span className="text-[12.5px] text-passive">{subtitle}</span>
		</div>
	);
}
