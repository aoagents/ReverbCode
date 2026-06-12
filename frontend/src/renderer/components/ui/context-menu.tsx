import { LoaderCircle } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { cn } from "../../lib/utils";

/**
 * Lightweight right-click menu for sidebar rows. Items run async actions
 * in-place: a `confirmLabel` arms a destructive item on first click, the busy
 * row shows a spinner, and failures render inline so the user sees why.
 */
export type ContextMenuItem = {
	id: string;
	label: string;
	icon?: React.ReactNode;
	tone?: "default" | "danger";
	disabled?: boolean;
	/** Two-step destructive confirm: first click re-labels, second click runs. */
	confirmLabel?: string;
	onSelect: () => void | Promise<void>;
};

export type ContextMenuState = {
	x: number;
	y: number;
	items: ContextMenuItem[];
};

export function useContextMenu() {
	const [menu, setMenu] = useState<ContextMenuState | null>(null);

	const openMenu = useCallback((event: React.MouseEvent, items: ContextMenuItem[]) => {
		event.preventDefault();
		event.stopPropagation();
		setMenu({ x: event.clientX, y: event.clientY, items });
	}, []);

	const closeMenu = useCallback(() => setMenu(null), []);

	return { menu, openMenu, closeMenu };
}

const MENU_WIDTH = 208;

export function ContextMenu({ menu, onClose }: { menu: ContextMenuState; onClose: () => void }) {
	const [armedId, setArmedId] = useState<string | null>(null);
	const [busyId, setBusyId] = useState<string | null>(null);
	const [error, setError] = useState<string | null>(null);
	const menuRef = useRef<HTMLDivElement | null>(null);

	useEffect(() => {
		const handleKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Escape") onClose();
		};
		// Capture phase: the focused xterm pane stops propagation of keys it
		// handles, so a bubble-phase listener would never see Escape.
		window.addEventListener("keydown", handleKeyDown, true);
		return () => window.removeEventListener("keydown", handleKeyDown, true);
	}, [onClose]);

	// An armed destructive item decays back to its resting label after 3s, so a
	// pause after an accidental first click can't leave a live trigger behind
	// for a much-later mistaken second click.
	useEffect(() => {
		if (!armedId) return;
		const timer = window.setTimeout(() => setArmedId(null), 3000);
		return () => window.clearTimeout(timer);
	}, [armedId]);

	// Keep the menu on-screen for rows near the window edges.
	const left = Math.min(menu.x, window.innerWidth - MENU_WIDTH - 8);
	const estimatedHeight = menu.items.length * 30 + 12;
	const top = Math.min(menu.y, window.innerHeight - estimatedHeight - 8);

	const run = async (item: ContextMenuItem) => {
		if (item.disabled || busyId) return;
		if (item.confirmLabel && armedId !== item.id) {
			setArmedId(item.id);
			return;
		}
		setError(null);
		setBusyId(item.id);
		try {
			await item.onSelect();
			onClose();
		} catch (err) {
			setError(err instanceof Error ? err.message : "Action failed");
			setArmedId(null);
		} finally {
			setBusyId(null);
		}
	};

	return (
		<div
			aria-hidden
			className="fixed inset-0 z-50"
			onClick={onClose}
			onContextMenu={(event) => {
				event.preventDefault();
				onClose();
			}}
		>
			<div
				className="absolute min-w-[208px] rounded-lg border border-border bg-raised p-1 shadow-lg"
				onClick={(event) => event.stopPropagation()}
				ref={menuRef}
				role="menu"
				style={{ left, top }}
			>
				{menu.items.map((item) => {
					const armed = armedId === item.id;
					const busy = busyId === item.id;
					return (
						<button
							className={cn(
								"flex h-[30px] w-full items-center gap-2 rounded-md px-2 text-left text-[12.5px] transition-colors",
								item.tone === "danger" || armed ? "text-error hover:bg-error/10" : "text-foreground hover:bg-overlay",
								item.disabled && "cursor-default opacity-40 hover:bg-transparent",
							)}
							disabled={item.disabled || Boolean(busyId)}
							key={item.id}
							onClick={() => void run(item)}
							role="menuitem"
							type="button"
						>
							<span className="grid h-3.5 w-3.5 shrink-0 place-items-center [&_svg]:h-3.5 [&_svg]:w-3.5">
								{busy ? <LoaderCircle className="animate-spin" aria-hidden="true" /> : item.icon}
							</span>
							<span className="min-w-0 flex-1 truncate">
								{armed && item.confirmLabel ? item.confirmLabel : item.label}
							</span>
						</button>
					);
				})}
				{error && (
					<p className="max-w-[240px] px-2 py-1.5 text-[11.5px] leading-snug text-error" role="alert">
						{error}
					</p>
				)}
			</div>
		</div>
	);
}
