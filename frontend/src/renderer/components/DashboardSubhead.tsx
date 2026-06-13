import { Fragment } from "react";
import {
	Breadcrumb,
	BreadcrumbItem,
	BreadcrumbList,
	BreadcrumbPage,
	BreadcrumbSeparator,
} from "./ui/breadcrumb";

// The board subhead (mc-board .dashboard-main__subhead): a 21px bold title with
// a muted one-line subtitle, optionally a trailing count.
export function DashboardSubhead({
	title,
	subtitle,
	count,
	breadcrumbs,
}: {
	title: string;
	subtitle?: string;
	count?: number;
	breadcrumbs?: readonly string[];
}) {
	return (
		// pt aligns the title's vertical center with the sidebar brand row
		// ("Agent Orchestrator") across the shell divider.
		<div className="flex items-baseline gap-3 px-[18px] pt-[9px]">
			{breadcrumbs ? (
				<Breadcrumb>
					<BreadcrumbList className="text-[21px] font-bold tracking-[-0.025em]">
						{breadcrumbs.map((item, index) => (
							<Fragment key={`${item}-${index}`}>
								{index > 0 && <BreadcrumbSeparator />}
								<BreadcrumbItem>
									{index === breadcrumbs.length - 1 ? (
										<BreadcrumbPage>{item}</BreadcrumbPage>
									) : (
										<span className="truncate text-passive">{item}</span>
									)}
								</BreadcrumbItem>
							</Fragment>
						))}
					</BreadcrumbList>
				</Breadcrumb>
			) : (
				<h1 className="text-[21px] font-bold tracking-[-0.025em] text-foreground">{title}</h1>
			)}
			{typeof count === "number" && <span className="font-mono text-[13px] text-passive">{count}</span>}
			{subtitle && <span className="text-[12.5px] text-passive">{subtitle}</span>}
		</div>
	);
}
