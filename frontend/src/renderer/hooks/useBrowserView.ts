import { useCallback, useEffect, useRef, useState } from "react";
import type { BrowserNavState, BrowserRect } from "../../main/browser-view-host";

export type { BrowserNavState };

type UseBrowserViewOptions = {
	sessionId: string;
	active: boolean;
	poppedOut: boolean;
	/**
	 * Preview target driven by the daemon (via `ao preview`, streamed over CDC).
	 * When set, the view navigates here automatically; an empty value clears it.
	 */
	previewUrl?: string;
	/**
	 * Monotonic counter the daemon bumps on every `ao preview` call, even when
	 * previewUrl is unchanged. The view re-navigates whenever it advances, so a
	 * repeated `ao preview <same-url>` still refreshes (and CDC replays of an
	 * unrelated session update, which leave it unchanged, are ignored).
	 */
	previewRevision?: number;
};

export type BrowserViewModel = {
	viewId: string;
	navState: BrowserNavState;
	slotRef: (node: HTMLDivElement | null) => void;
	navigate: (url: string) => Promise<void>;
	goBack: () => Promise<void>;
	goForward: () => Promise<void>;
	reload: () => Promise<void>;
	stop: () => Promise<void>;
	destroy: () => void;
};

const EMPTY_NAV_STATE: BrowserNavState = {
	viewId: "",
	url: "",
	title: "",
	canGoBack: false,
	canGoForward: false,
	isLoading: false,
};

const HIDDEN_RECT: BrowserRect = { x: 0, y: 0, width: 0, height: 0 };

export function useBrowserView({
	sessionId,
	active,
	poppedOut,
	previewUrl,
	previewRevision,
}: UseBrowserViewOptions): BrowserViewModel {
	const [viewId, setViewId] = useState("");
	const [navState, setNavState] = useState<BrowserNavState>(EMPTY_NAV_STATE);
	const slotNodeRef = useRef<HTMLDivElement | null>(null);
	const viewIdRef = useRef("");
	const activeRef = useRef(active);
	const frameRef = useRef<number | null>(null);
	const observerRef = useRef<ResizeObserver | null>(null);
	const previewRevisionRef = useRef<number | null>(null);

	useEffect(() => {
		activeRef.current = active;
	}, [active]);

	const sendHiddenBounds = useCallback((id = viewIdRef.current) => {
		if (!id) return;
		window.ao?.browser.setBounds({ viewId: id, rect: HIDDEN_RECT, visible: false });
	}, []);

	const measureAndSend = useCallback(() => {
		frameRef.current = null;
		const id = viewIdRef.current;
		const node = slotNodeRef.current;
		if (!id) return;
		if (!activeRef.current || !node || !node.isConnected) {
			sendHiddenBounds(id);
			return;
		}
		const rect = node.getBoundingClientRect();
		const payload = {
			viewId: id,
			rect: {
				x: rect.x,
				y: rect.y,
				width: rect.width,
				height: rect.height,
			},
			visible: rect.width > 0 && rect.height > 0,
		};
		window.ao?.browser.setBounds(payload);
	}, [sendHiddenBounds]);

	const cancelScheduledMeasure = useCallback(() => {
		if (frameRef.current === null) return;
		if (window.cancelAnimationFrame) {
			window.cancelAnimationFrame(frameRef.current);
		}
		window.clearTimeout(frameRef.current);
		frameRef.current = null;
	}, []);

	const scheduleMeasure = useCallback(() => {
		if (frameRef.current !== null) return;
		frameRef.current = window.requestAnimationFrame
			? window.requestAnimationFrame(() => measureAndSend())
			: window.setTimeout(() => measureAndSend(), 16);
	}, [measureAndSend]);

	const slotRef = useCallback(
		(node: HTMLDivElement | null) => {
			observerRef.current?.disconnect();
			slotNodeRef.current = node;
			if (node) {
				const observer = new ResizeObserver(scheduleMeasure);
				observer.observe(node);
				observerRef.current = observer;
			}
			scheduleMeasure();
		},
		[scheduleMeasure],
	);

	useEffect(() => {
		let disposed = false;
		window.ao?.browser.ensure(sessionId).then((state) => {
			if (disposed) return;
			viewIdRef.current = state.viewId;
			setViewId(state.viewId);
			setNavState(state);
			scheduleMeasure();
		});
		return () => {
			disposed = true;
			const id = viewIdRef.current;
			if (id) {
				sendHiddenBounds(id);
			}
			viewIdRef.current = "";
		};
	}, [scheduleMeasure, sendHiddenBounds, sessionId]);

	useEffect(() => {
		return window.ao?.browser.onNavState((state) => {
			if (state.viewId !== viewIdRef.current) return;
			setNavState(state);
		});
	}, []);

	useEffect(() => {
		if (active) {
			scheduleMeasure();
		} else {
			sendHiddenBounds();
		}
	}, [active, poppedOut, scheduleMeasure, sendHiddenBounds]);

	useEffect(() => {
		const handle = () => scheduleMeasure();
		window.addEventListener("resize", handle);
		window.addEventListener("scroll", handle, true);
		return () => {
			window.removeEventListener("resize", handle);
			window.removeEventListener("scroll", handle, true);
			observerRef.current?.disconnect();
			cancelScheduledMeasure();
		};
	}, [cancelScheduledMeasure, scheduleMeasure]);

	const withView = useCallback(async (fn: (id: string) => Promise<BrowserNavState | void>) => {
		const id = viewIdRef.current;
		if (!id) return;
		const next = await fn(id);
		if (next) setNavState(next);
	}, []);

	const navigate = useCallback(
		(url: string) => withView((id) => window.ao!.browser.navigate({ viewId: id, url })),
		[withView],
	);

	const clear = useCallback(() => withView((id) => window.ao!.browser.clear(id)), [withView]);

	// Drive the view from the daemon-set preview target, keyed on the preview
	// revision (bumped on every `ao preview` call). Acting on the revision rather
	// than the URL means a repeated `ao preview <same-url>` still refreshes, an
	// `ao preview clear` (empty URL) blanks the view, and CDC replays of
	// unrelated session updates (revision unchanged) are ignored — so the panel
	// never reloads on an unrelated activity flip.
	useEffect(() => {
		if (!viewId) return;
		const revision = previewRevision ?? 0;
		if (previewRevisionRef.current === revision) return;
		previewRevisionRef.current = revision;
		const target = previewUrl?.trim();
		if (target) {
			void navigate(target);
		} else if (revision > 0) {
			void clear();
		}
	}, [clear, navigate, previewRevision, previewUrl, viewId]);

	const destroy = useCallback(() => {
		const id = viewIdRef.current;
		if (!id) return;
		sendHiddenBounds(id);
		window.ao?.browser.destroy(id);
		viewIdRef.current = "";
	}, [sendHiddenBounds]);

	return {
		viewId,
		navState,
		slotRef,
		navigate,
		goBack: () => withView((id) => window.ao!.browser.goBack(id)),
		goForward: () => withView((id) => window.ao!.browser.goForward(id)),
		reload: () => withView((id) => window.ao!.browser.reload(id)),
		stop: () => withView((id) => window.ao!.browser.stop(id)),
		destroy,
	};
}
