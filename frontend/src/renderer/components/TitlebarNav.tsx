import { useCanGoBack, useRouter, useRouterState } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, PanelLeft } from "lucide-react";
import { useUiStore } from "../stores/ui-store";

const isMac = typeof navigator !== "undefined" && /Mac|iPod|iPhone|iPad/.test(navigator.userAgent);
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

// macOS-only titlebar cluster (sidebar toggle + history arrows) pinned beside
// the traffic lights, VS Code-style. Approved divergence from the web
// reference, which has no window chrome (DESIGN.md banner, 2026-06-10).
// Rendered once by the shell as a fixed overlay (.titlebar-nav in styles.css)
// so the buttons occupy the exact same spot whether the sidebar is expanded
// or collapsed; the collapsed-rail topbars pad past it (.is-under-titlebar-nav).
export function TitlebarNav() {
	const { isSidebarOpen, toggleSidebar } = useUiStore();
	const router = useRouter();
	const canGoBack = useCanGoBack();
	// No useCanGoForward in the installed router; derive it from the history
	// index the same way useCanGoBack does (any back/forward/push re-renders
	// via the location store, so history.length is read fresh).
	const canGoForward = useRouterState({
		select: (state) => state.location.state.__TSR_index < router.history.length - 1,
	});

	if (!isMac) return null;

	return (
		<div className="titlebar-nav" style={noDragStyle}>
			<TitlebarButton
				label={isSidebarOpen ? "Collapse sidebar" : "Expand sidebar"}
				onClick={toggleSidebar}
				title={`${isSidebarOpen ? "Collapse" : "Expand"} sidebar · ⌘B`}
			>
				<PanelLeft className="h-[15px] w-[15px]" aria-hidden="true" />
			</TitlebarButton>
			<TitlebarButton disabled={!canGoBack} label="Go back" onClick={() => router.history.back()} title="Go back">
				<ArrowLeft className="h-[15px] w-[15px]" aria-hidden="true" />
			</TitlebarButton>
			<TitlebarButton
				disabled={!canGoForward}
				label="Go forward"
				onClick={() => router.history.forward()}
				title="Go forward"
			>
				<ArrowRight className="h-[15px] w-[15px]" aria-hidden="true" />
			</TitlebarButton>
		</div>
	);
}

function TitlebarButton({
	label,
	title,
	disabled,
	onClick,
	children,
}: {
	label: string;
	title: string;
	disabled?: boolean;
	onClick: () => void;
	children: React.ReactNode;
}) {
	return (
		<button
			aria-label={label}
			className="titlebar-nav__btn grid place-items-center rounded-md text-passive transition-colors hover:bg-interactive-hover hover:text-muted-foreground disabled:pointer-events-none disabled:opacity-45"
			disabled={disabled}
			onClick={onClick}
			style={noDragStyle}
			title={title}
			type="button"
		>
			{children}
		</button>
	);
}
