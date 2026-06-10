"use client";

import { useEffect, useRef, type PointerEvent as ReactPointerEvent } from "react";

const dim = "text-[var(--landing-muted-dim)]";
const fg = "text-[var(--landing-fg)]/80";
const ok = "text-[rgba(134,239,172,0.8)]";

type UseCase = {
	eyebrow: string;
	title: string;
	desc: string;
	prefix: "$" | "⟡";
	cmd: string;
	outcome: string;
};

// Real, grounded use cases — real ao commands, reaction keys, and lifecycle states.
const cases: UseCase[] = [
	{
		eyebrow: "Backlog",
		title: "Clear it overnight",
		desc: "One agent per issue, each in its own git worktree, all running at once.",
		prefix: "$",
		cmd: "ao batch-spawn 142 143 144 145",
		outcome: "4 worktrees · 4 PRs",
	},
	{
		eyebrow: "CI recovery",
		title: "Self-healing builds",
		desc: "A check goes red; the agent reads the logs, pushes a fix, and waits for green.",
		prefix: "⟡",
		cmd: "reaction · ci-failed",
		outcome: "ci_failed → mergeable",
	},
	{
		eyebrow: "Review loop",
		title: "Answers its own reviews",
		desc: "Comments land; the agent addresses each one and re-requests review.",
		prefix: "⟡",
		cmd: "reaction · changes-requested",
		outcome: "changes_requested → approved",
	},
	{
		eyebrow: "Migration",
		title: "Grinds through the long ones",
		desc: "Hand one agent a sweeping change and let it work file by file until tests pass.",
		prefix: "$",
		cmd: "ao spawn 305 --agent claude-code",
		outcome: "23 files · tests green",
	},
	{
		eyebrow: "Per-role",
		title: "Right model per job",
		desc: "Claude Code orchestrates, Codex does the work. Pick the tool per task.",
		prefix: "$",
		cmd: "ao spawn 88 --agent codex",
		outcome: "codex #88 · claude-code #91",
	},
	{
		eyebrow: "Multi-project",
		title: "Every repo, one screen",
		desc: "Register all your repos and supervise their agents from a single dashboard.",
		prefix: "$",
		cmd: "ao start",
		outcome: "3 projects · one dashboard",
	},
];

const N = cases.length;
const THETA = 360 / N;
const RADIUS = 440;
const CARD_W = 360;
const CARD_H = 440;

export function LandingUseCases() {
	const viewportRef = useRef<HTMLDivElement>(null);
	const ringRef = useRef<HTMLDivElement>(null);
	const cardRefs = useRef<(HTMLDivElement | null)[]>([]);

	const angle = useRef(0);
	const dragging = useRef(false);
	const paused = useRef(false);
	const reduced = useRef(false);
	const start = useRef({ x: 0, a: 0 });

	// rAF loop — rotate the ring and fade/scale each card by how far it faces the
	// camera. Imperative (no setState) so 60fps stays smooth and re-render-free.
	useEffect(() => {
		reduced.current = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
		let raf = 0;
		const loop = () => {
			if (!dragging.current && !paused.current && !reduced.current) {
				angle.current += 0.12;
			}
			const a = angle.current;
			if (ringRef.current) {
				ringRef.current.style.transform = `translateZ(-${RADIUS}px) rotateY(${a}deg)`;
			}
			cardRefs.current.forEach((el, i) => {
				if (!el) return;
				const facing = Math.cos(((i * THETA + a) * Math.PI) / 180);
				const vis = Math.max(facing, 0);
				el.style.opacity = `${0.2 + 0.8 * vis}`;
				el.style.transform = `rotateY(${i * THETA}deg) translateZ(${RADIUS}px) scale(${0.9 + 0.1 * vis})`;
			});
			raf = requestAnimationFrame(loop);
		};
		raf = requestAnimationFrame(loop);
		return () => cancelAnimationFrame(raf);
	}, []);

	const onPointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
		dragging.current = true;
		start.current = { x: e.clientX, a: angle.current };
		e.currentTarget.setPointerCapture(e.pointerId);
		if (viewportRef.current) viewportRef.current.style.cursor = "grabbing";
	};
	const onPointerMove = (e: ReactPointerEvent<HTMLDivElement>) => {
		if (!dragging.current) return;
		angle.current = start.current.a + (e.clientX - start.current.x) * 0.4;
	};
	const onPointerUp = () => {
		dragging.current = false;
		if (viewportRef.current) viewportRef.current.style.cursor = "grab";
	};

	return (
		<section className="py-[100px] px-6 max-w-[72rem] mx-auto">
			<div className="landing-reveal text-center">
				<div className="text-xs tracking-[0.15em] uppercase text-[var(--landing-muted-dim)] mb-6 font-mono">
					Use cases
				</div>
				<h2 className="font-sans font-[680] text-[clamp(1.375rem,3vw,2rem)] leading-[1.1] tracking-[-1.5px] mb-4">
					One orchestrator, many jobs
				</h2>
				<p className="text-[var(--landing-muted)] text-[0.9375rem] leading-[1.6] max-w-[34rem] mx-auto mb-12">
					Point AO at the work and walk away — drag to explore what a single run can do.
				</p>
			</div>

			<div
				ref={viewportRef}
				onMouseEnter={() => (paused.current = true)}
				onMouseLeave={() => {
					paused.current = false;
					onPointerUp();
				}}
				onPointerDown={onPointerDown}
				onPointerMove={onPointerMove}
				onPointerUp={onPointerUp}
				className="landing-reveal relative mx-auto select-none"
				style={{
					perspective: "1900px",
					height: `${CARD_H + 80}px`,
					maxWidth: "1120px",
					cursor: "grab",
					touchAction: "pan-y",
					WebkitMaskImage: "linear-gradient(to right, transparent, #000 16%, #000 84%, transparent)",
					maskImage: "linear-gradient(to right, transparent, #000 16%, #000 84%, transparent)",
				}}
			>
				<div
					ref={ringRef}
					style={{
						position: "absolute",
						inset: 0,
						transformStyle: "preserve-3d",
					}}
				>
					{cases.map((c, i) => (
						<div
							key={c.eyebrow}
							ref={(el) => {
								cardRefs.current[i] = el;
							}}
							style={{
								position: "absolute",
								left: "50%",
								top: "50%",
								width: `${CARD_W}px`,
								height: `${CARD_H}px`,
								marginLeft: `-${CARD_W / 2}px`,
								marginTop: `-${CARD_H / 2}px`,
								backfaceVisibility: "hidden",
							}}
						>
							<div
								className="landing-card rounded-2xl"
								style={{
									width: "100%",
									height: "100%",
									padding: "1.875rem",
									display: "flex",
									flexDirection: "column",
								}}
							>
								<div className="font-mono text-[0.6875rem] tracking-[0.12em] uppercase text-[var(--landing-accent)] opacity-80">
									{c.eyebrow}
								</div>
								<h3 className="font-sans font-[680] text-[1.3125rem] tracking-tight" style={{ marginTop: "1rem" }}>
									{c.title}
								</h3>
								<p
									className="text-[var(--landing-muted)] text-[0.9375rem] leading-[1.65]"
									style={{ marginTop: "0.625rem" }}
								>
									{c.desc}
								</p>
								<div
									className="font-mono text-[0.6875rem] leading-[2] bg-black/30 rounded-lg overflow-hidden"
									style={{ marginTop: "auto", padding: "0.75rem 0.875rem" }}
								>
									<div className="whitespace-nowrap overflow-hidden text-ellipsis">
										<span className={dim}>{c.prefix}</span> <span className={fg}>{c.cmd}</span>
									</div>
									<div className={`whitespace-nowrap overflow-hidden text-ellipsis ${ok}`}>→ {c.outcome}</div>
								</div>
							</div>
						</div>
					))}
				</div>
			</div>
		</section>
	);
}
