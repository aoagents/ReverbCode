"use client";

import { useEffect, useRef, useState } from "react";

// The session lifecycle as a scrubbing ruler-dial. One agent's journey from a
// fresh issue to a merged PR — the real canonical states. The strip slides so
// the active milestone sits under the fixed center line; auto-loops, draggable,
// arrow-key navigable.
type Milestone = {
	key: string;
	label: string;
	desc: string;
	icon: "spawn" | "work" | "pr" | "review" | "mergeable" | "merged";
};

const milestones: Milestone[] = [
	{
		key: "spawning",
		label: "Spawn",
		icon: "spawn",
		desc: "Each issue spawns an agent in its own git worktree — isolated branch, isolated context.",
	},
	{
		key: "working",
		label: "Work",
		icon: "work",
		desc: "The agent writes code, runs the test suite, and commits. Watch it live or let it run.",
	},
	{
		key: "pr_open",
		label: "Open PR",
		icon: "pr",
		desc: "Work is pushed and a pull request opens against main with a summary of the changes.",
	},
	{
		key: "review",
		label: "CI & review",
		icon: "review",
		desc: "CI fails? It reads the logs and pushes a fix. Review comments land? It addresses them.",
	},
	{
		key: "mergeable",
		label: "Mergeable",
		icon: "mergeable",
		desc: "Green checks, approvals in. The PR settles into a clean, mergeable state.",
	},
	{
		key: "merged",
		label: "Merged",
		icon: "merged",
		desc: "It lands on main, the worktree is archived, and the session is marked done.",
	},
];

// Ruler geometry (viewBox units)
const W = 760;
const H = 150;
const CENTER_X = W / 2;
const TICKS_PER_STEP = 16;
const TICK_GAP = 11;
const PEAK_Y = 34;
const CURV = 0.00026;
const MINOR_LEN = 13;
const MAJOR_LEN = 26;
const MAX_K = (milestones.length - 1) * TICKS_PER_STEP;
const STEP_MS = 2800;

const arcTop = (dx: number) => PEAK_Y + CURV * dx * dx;

export function LandingWorkflow() {
	const [active, setActive] = useState(0);
	const [pos, setPos] = useState(0); // current center position in tick units
	const [show, setShow] = useState(true);
	const [paused, setPaused] = useState(false);
	const [inView, setInView] = useState(false);

	const wrapRef = useRef<HTMLDivElement>(null);
	const svgWrapRef = useRef<HTMLDivElement>(null);
	const posRef = useRef(0);
	const rafRef = useRef(0);
	const drag = useRef<{ active: boolean; startX: number; startPos: number; moved: boolean }>({
		active: false,
		startX: 0,
		startPos: 0,
		moved: false,
	});

	useEffect(() => {
		const el = wrapRef.current;
		if (!el) return;
		const ob = new IntersectionObserver(([entry]) => entry.isIntersecting && setInView(true), { threshold: 0.25 });
		ob.observe(el);
		return () => ob.disconnect();
	}, []);

	const tweenTo = (target: number) => {
		cancelAnimationFrame(rafRef.current);
		const start = posRef.current;
		const dist = target - start;
		if (Math.abs(dist) < 0.001) return;
		const dur = 600;
		let t0: number | null = null;
		const step = (now: number) => {
			if (t0 === null) t0 = now;
			const p = Math.min((now - t0) / dur, 1);
			const e = 1 - Math.pow(1 - p, 3);
			const v = start + dist * e;
			posRef.current = v;
			setPos(v);
			if (p < 1) rafRef.current = requestAnimationFrame(step);
		};
		rafRef.current = requestAnimationFrame(step);
	};

	// Slide to the active milestone whenever it changes
	useEffect(() => {
		tweenTo(active * TICKS_PER_STEP);
		setShow(false);
		const id = window.setTimeout(() => setShow(true), 200);
		return () => window.clearTimeout(id);
	}, [active]);

	// Auto-loop
	useEffect(() => {
		if (!inView || paused) return;
		const t = window.setTimeout(() => setActive((a) => (a + 1) % milestones.length), STEP_MS);
		return () => window.clearTimeout(t);
	}, [active, paused, inView]);

	const settle = (rawPos: number) => {
		const nearest = Math.max(0, Math.min(milestones.length - 1, Math.round(rawPos / TICKS_PER_STEP)));
		if (nearest === active) tweenTo(nearest * TICKS_PER_STEP);
		setActive(nearest);
	};

	// Drag to scrub
	const onPointerDown = (e: React.PointerEvent) => {
		drag.current = { active: true, startX: e.clientX, startPos: posRef.current, moved: false };
		(e.target as HTMLElement).setPointerCapture?.(e.pointerId);
		setPaused(true);
		cancelAnimationFrame(rafRef.current);
	};
	const onPointerMove = (e: React.PointerEvent) => {
		if (!drag.current.active) return;
		const widthPx = svgWrapRef.current?.clientWidth ?? W;
		const scale = W / widthPx;
		const dxTicks = ((e.clientX - drag.current.startX) * scale) / TICK_GAP;
		if (Math.abs(dxTicks) > 0.4) drag.current.moved = true;
		const v = Math.max(-2, Math.min(MAX_K + 2, drag.current.startPos - dxTicks));
		posRef.current = v;
		setPos(v);
	};
	const onPointerUp = () => {
		if (!drag.current.active) return;
		drag.current.active = false;
		settle(posRef.current);
		setPaused(false);
	};

	const onKeyDown = (e: React.KeyboardEvent) => {
		if (e.key === "ArrowRight") {
			e.preventDefault();
			setActive((a) => Math.min(milestones.length - 1, a + 1));
		} else if (e.key === "ArrowLeft") {
			e.preventDefault();
			setActive((a) => Math.max(0, a - 1));
		}
	};

	// Build the visible ticks
	const ticks: { k: number; x: number; yTop: number; len: number; major: boolean; opacity: number }[] = [];
	const lo = Math.floor(pos) - 40;
	const hi = Math.ceil(pos) + 40;
	for (let k = lo; k <= hi; k++) {
		if (k < 0 || k > MAX_K) continue;
		const x = CENTER_X + (k - pos) * TICK_GAP;
		if (x < 24 || x > W - 24) continue;
		const dx = x - CENTER_X;
		const major = k % TICKS_PER_STEP === 0;
		const edgeFade = Math.max(0.08, 1 - Math.abs(dx) / (W / 2 + 40));
		ticks.push({ k, x, yTop: arcTop(dx), len: major ? MAJOR_LEN : MINOR_LEN, major, opacity: edgeFade });
	}

	const cur = milestones[active];

	return (
		<section ref={wrapRef} className="py-[100px] px-6 max-w-[72rem] mx-auto">
			<div className="landing-reveal">
				<div className="text-xs tracking-[0.15em] uppercase text-[var(--landing-muted-dim)] mb-6 font-mono">
					Lifecycle
				</div>
				<h2 className="font-sans font-[680] text-[clamp(1.375rem,3vw,2rem)] leading-[1.1] tracking-[-1.5px] mb-4">
					From issue to merged PR
				</h2>
				<p className="text-[var(--landing-muted)] text-[0.9375rem] leading-[1.6] max-w-[34rem] mb-12">
					Every session walks the same path — spawned in isolation, working in parallel, landing on{" "}
					<span className="font-mono text-[var(--landing-fg)]">main</span> on its own.
				</p>
			</div>

			<div
				className="landing-card rounded-2xl border-[var(--landing-border-subtle)] px-6 sm:px-10 pt-10 pb-12 select-none outline-none"
				tabIndex={0}
				role="group"
				aria-label="Session lifecycle timeline"
				onKeyDown={onKeyDown}
				onMouseEnter={() => setPaused(true)}
				onMouseLeave={() => setPaused(false)}
			>
				{/* Active icon + label */}
				<div
					className="flex flex-col items-center text-center mb-2"
					style={{ opacity: show ? 1 : 0, transition: "opacity 0.35s ease" }}
				>
					<LifecycleIcon kind={cur.icon} />
					<div className="font-sans font-[680] tracking-tight text-[1.5rem] tracking-[-0.5px] mt-3">{cur.label}</div>
					<div className="font-mono text-[0.6875rem] tracking-[0.08em] text-[var(--landing-accent)] opacity-80 mt-1">
						{cur.key}
					</div>
				</div>

				{/* Ruler */}
				<div
					ref={svgWrapRef}
					className="cursor-grab active:cursor-grabbing touch-none"
					onPointerDown={onPointerDown}
					onPointerMove={onPointerMove}
					onPointerUp={onPointerUp}
					onPointerCancel={onPointerUp}
				>
					<svg viewBox={`0 0 ${W} ${H}`} className="w-full h-auto" role="img" aria-label="Lifecycle scrubber">
						{/* center indicator */}
						<line
							x1={CENTER_X}
							y1={4}
							x2={CENTER_X}
							y2={H - 8}
							stroke="var(--landing-accent)"
							strokeWidth={2}
							strokeLinecap="round"
						/>
						<circle cx={CENTER_X} cy={arcTop(0)} r={4} fill="var(--landing-accent)" />

						{/* ticks */}
						{ticks.map((t) => (
							<line
								key={t.k}
								x1={t.x}
								y1={t.yTop}
								x2={t.x}
								y2={t.yTop + t.len}
								stroke={t.major ? "var(--landing-border-strong)" : "var(--landing-border-default)"}
								strokeWidth={t.major ? 1.6 : 1}
								strokeLinecap="round"
								style={{ opacity: t.opacity }}
							/>
						))}

						{/* milestone labels under their major tick (except the centered one) */}
						{ticks
							.filter((t) => t.major)
							.map((t) => {
								const idx = t.k / TICKS_PER_STEP;
								const isActive = idx === active;
								return (
									<text
										key={`lbl-${t.k}`}
										x={t.x}
										y={t.yTop + MAJOR_LEN + 16}
										textAnchor="middle"
										className="font-mono"
										fontSize={9}
										fill="var(--landing-muted)"
										style={{ opacity: isActive ? 0 : t.opacity * 0.7, transition: "opacity 0.3s ease" }}
									>
										{milestones[idx].label}
									</text>
								);
							})}
					</svg>
				</div>

				{/* Active description */}
				<div
					className="text-center max-w-[34rem] mx-auto mt-2"
					style={{ opacity: show ? 1 : 0, transition: "opacity 0.35s ease" }}
				>
					<p className="text-[var(--landing-muted)] text-[0.9375rem] leading-[1.6]">{cur.desc}</p>
				</div>
			</div>
		</section>
	);
}

function LifecycleIcon({ kind }: { kind: Milestone["icon"] }) {
	const common = {
		width: 30,
		height: 30,
		viewBox: "0 0 24 24",
		fill: "none",
		stroke: "var(--landing-accent)",
		strokeWidth: 1.6,
		strokeLinecap: "round" as const,
		strokeLinejoin: "round" as const,
	};
	switch (kind) {
		case "spawn":
			return (
				<svg {...common}>
					<path d="M12 20v-7" />
					<path d="M12 13c0-3 2-5 5-5 0 3-2 5-5 5Z" />
					<path d="M12 14c0-2.5-1.8-4.2-4.2-4.2C7.8 12.3 9.6 14 12 14Z" />
				</svg>
			);
		case "work":
			return (
				<svg {...common}>
					<rect x="3" y="4" width="18" height="16" rx="2" />
					<path d="M7 9l3 3-3 3" />
					<path d="M13 15h4" />
				</svg>
			);
		case "pr":
			return (
				<svg {...common}>
					<circle cx="6" cy="18" r="2.5" />
					<circle cx="6" cy="6" r="2.5" />
					<circle cx="18" cy="8" r="2.5" />
					<path d="M6 8.5v7" />
					<path d="M18 10.5c0 4-6 2.5-6 6" />
					<path d="M18 5.5V3.5M16.5 5h3" />
				</svg>
			);
		case "review":
			return (
				<svg {...common}>
					<path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6-10-6-10-6Z" />
					<circle cx="12" cy="12" r="2.5" />
				</svg>
			);
		case "mergeable":
			return (
				<svg {...common}>
					<circle cx="12" cy="12" r="9" />
					<path d="M8 12l2.5 2.5L16 9" />
				</svg>
			);
		case "merged":
			return (
				<svg {...common}>
					<circle cx="6" cy="6" r="2.5" />
					<circle cx="6" cy="18" r="2.5" />
					<circle cx="18" cy="12" r="2.5" />
					<path d="M6 8.5v7" />
					<path d="M8.5 6.5c5 0 2 5.5 7 5.5" />
				</svg>
			);
	}
}
