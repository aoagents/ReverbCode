"use client";

import { useEffect, useRef, useState } from "react";

const DURATION_MS = 3000;

const steps = [
	{
		n: "01",
		title: "Configure & assign",
		titleEm: "assign",
		desc: "Point Agent Orchestrator at your repo with a YAML config. Choose your agent, set up trackers and notifiers. One file, full control.",
		tags: ["YAML", "Plugins", "Trackers"],
		kind: "cli" as const,
	},
	{
		n: "02",
		title: "Agents work",
		titleEm: "work",
		desc: "Each agent spawns in an isolated worktree. They write code, create PRs, run tests, and fix failures. Monitor everything from the live dashboard, or let them run.",
		tags: ["Worktrees", "Live dashboard", "Parallel"],
		kind: "dashboard" as const,
	},
	{
		n: "03",
		title: "PRs land",
		titleEm: "land",
		desc: "Agents create pull requests, address review comments, fix CI failures, and get them to mergeable state. Your morning starts with merged PRs, not a backlog.",
		tags: ["Pull requests", "CI fixes", "Review"],
		kind: "prs" as const,
	},
];

export function LandingHowItWorks() {
	const [active, setActive] = useState(0);
	const [progress, setProgress] = useState(0);
	const [isDesktop, setIsDesktop] = useState(true);
	const pausedRef = useRef(false);
	const startRef = useRef<number | null>(null);

	useEffect(() => {
		const mq = window.matchMedia("(min-width: 768px)");
		const apply = () => setIsDesktop(mq.matches);
		apply();
		mq.addEventListener("change", apply);
		return () => mq.removeEventListener("change", apply);
	}, []);

	useEffect(() => {
		let raf = 0;
		const tick = (now: number) => {
			if (startRef.current === null) startRef.current = now;
			if (!pausedRef.current) {
				const p = Math.min((now - startRef.current) / DURATION_MS, 1);
				setProgress(p);
				if (p >= 1) {
					startRef.current = now;
					setActive((a) => (a + 1) % steps.length);
					setProgress(0);
				}
			} else {
				startRef.current = now - progress * DURATION_MS;
			}
			raf = requestAnimationFrame(tick);
		};
		raf = requestAnimationFrame(tick);
		return () => cancelAnimationFrame(raf);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [active]);

	const select = (i: number) => {
		if (i === active) return;
		startRef.current = null;
		setProgress(0);
		setActive(i);
	};

	return (
		<section className="py-[120px] px-6 max-w-[72rem] mx-auto" id="how">
			<div className="landing-reveal">
				<div className="text-xs tracking-[0.15em] uppercase text-[var(--landing-muted)] opacity-60 mb-6">Process</div>
				<h2 className="font-sans font-[680] tracking-tight font-normal text-[clamp(1.375rem,3vw,2rem)] leading-[1.05] tracking-[-1.5px] mb-6">
					Three steps to <em className="italic text-[var(--landing-muted)]">orchestration</em>
				</h2>
			</div>

			<div
				className="landing-reveal mt-16 flex flex-col md:flex-row"
				style={isDesktop ? { minHeight: 540 } : undefined}
				onMouseEnter={() => (pausedRef.current = true)}
				onMouseLeave={() => (pausedRef.current = false)}
			>
				{steps.map((step, i) => {
					const isActive = i === active;
					return (
						<div
							key={step.n}
							role="button"
							tabIndex={0}
							aria-expanded={isActive}
							onClick={() => select(i)}
							onKeyDown={(e) => {
								if (e.key === "Enter" || e.key === " ") {
									e.preventDefault();
									select(i);
								}
							}}
							className="relative min-w-0 cursor-pointer overflow-hidden border-l border-[var(--landing-border-subtle)] pl-7 pr-5 py-2 first:border-l-0 first:pl-0 md:first:pl-7"
							style={{
								flex: isDesktop ? (isActive ? "1 1 0%" : "0 1 15rem") : "0 0 auto",
								transition: "flex 0.6s cubic-bezier(0.22,1,0.36,1)",
							}}
						>
							{/* Header — always visible */}
							<div
								className="font-mono text-[2.75rem] leading-none font-[680] tracking-tight mb-5"
								style={{
									color: "var(--landing-muted)",
									opacity: isActive ? 0.85 : 0.32,
									transition: "opacity 0.4s ease",
								}}
							>
								{step.n}
							</div>
							<h3
								className="font-sans font-[680] tracking-tight text-[1.375rem] leading-[1.15]"
								style={{
									color: isActive ? "var(--landing-fg)" : "var(--landing-muted)",
									transition: "color 0.4s ease",
									maxWidth: isActive ? "100%" : "11rem",
								}}
							>
								{step.title.replace(` ${step.titleEm}`, "")}{" "}
								<em className="italic text-[var(--landing-muted)]">{step.titleEm}</em>
							</h3>

							{/* Expanding body */}
							<div
								aria-hidden={!isActive}
								style={{
									opacity: isActive ? 1 : 0,
									maxHeight: isActive ? (isDesktop ? 1000 : 900) : 0,
									transition: isActive
										? "opacity 0.5s ease 0.15s, max-height 0.6s ease"
										: "opacity 0.25s ease, max-height 0.4s ease",
									overflow: "hidden",
								}}
							>
								<div className="flex gap-5 pt-7" style={{ minWidth: isDesktop ? "30rem" : undefined }}>
									{/* Vertical progress bar */}
									<div
										className="shrink-0 w-[3px] rounded-full overflow-hidden self-stretch"
										style={{ background: "var(--landing-border-default)" }}
									>
										<div
											style={{
												height: `${(isActive ? progress : 0) * 100}%`,
												background: "var(--landing-accent)",
												transition: "height 0.08s linear",
											}}
										/>
									</div>

									<div className="min-w-0">
										<p className="text-[var(--landing-muted)] text-[0.9375rem] leading-[1.7] max-w-[30rem] mb-6">
											{step.desc}
										</p>
										<div className="mb-6">
											{step.kind === "cli" && <CliDemo />}
											{step.kind === "dashboard" && <DashboardDemo />}
											{step.kind === "prs" && <PrsDemo />}
										</div>
										<div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[0.6875rem] tracking-[0.05em] uppercase text-[var(--landing-muted)] opacity-60">
											{step.tags.map((t, ti) => (
												<span key={t} className="flex items-center gap-3">
													{ti > 0 && <span className="opacity-40">·</span>}
													{t}
												</span>
											))}
										</div>
									</div>
								</div>
							</div>
						</div>
					);
				})}
			</div>
		</section>
	);
}

function CliDemo() {
	return (
		<div className="bg-black/40 rounded-xl overflow-hidden font-mono text-[0.8125rem]">
			<div className="flex items-center gap-2 px-4 py-3 bg-[var(--landing-surface)]">
				<div className="w-2.5 h-2.5 rounded-full bg-[rgba(255,240,220,0.12)]" />
				<div className="w-2.5 h-2.5 rounded-full bg-[rgba(255,240,220,0.12)]" />
				<div className="w-2.5 h-2.5 rounded-full bg-[rgba(255,240,220,0.12)]" />
			</div>
			<div className="px-5 py-4 leading-[1.8]">
				<div>
					<span className="text-[var(--landing-muted)]">$</span>{" "}
					<span className="text-white">ao batch-spawn 42 43 44 45 46</span>
				</div>
				<div className="text-[var(--landing-muted)] opacity-60">&nbsp;</div>
				<div className="text-[var(--landing-muted)] opacity-60">⟡ Loading config from agent-orchestrator.yaml</div>
				<div className="text-[var(--landing-muted)] opacity-60">⟡ Resolving 5 issues from GitHub</div>
				<div className="text-[var(--landing-muted)] opacity-60">⟡ Spawning sessions in worktrees...</div>
				<div className="text-[rgba(134,239,172,0.8)]">✓ Session s-001 spawned → issue #42</div>
				<div className="text-[rgba(134,239,172,0.8)]">✓ Session s-002 spawned → issue #43</div>
				<div className="text-[rgba(134,239,172,0.8)]">✓ Session s-003 spawned → issue #44</div>
				<div className="text-[rgba(134,239,172,0.8)]">✓ Session s-004 spawned → issue #45</div>
				<div className="text-[rgba(134,239,172,0.8)]">✓ Session s-005 spawned → issue #46</div>
				<div className="text-[var(--landing-muted)] opacity-60">&nbsp;</div>
				<div>
					<span className="landing-agent-dot mr-1.5" />
					<span className="text-[var(--landing-muted)] opacity-60">
						5 agents working · Dashboard → http://localhost:3000
					</span>
				</div>
			</div>
		</div>
	);
}

function DashboardDemo() {
	return (
		<div className="rounded-2xl overflow-hidden bg-black/30">
			<div className="flex items-center gap-2 px-4 py-2.5 bg-[var(--landing-card-bg)] border-b border-[var(--landing-border-subtle)]">
				<div className="w-2.5 h-2.5 rounded-full bg-[rgba(255,240,220,0.12)]" />
				<div className="w-2.5 h-2.5 rounded-full bg-[rgba(255,240,220,0.12)]" />
				<div className="w-2.5 h-2.5 rounded-full bg-[rgba(255,240,220,0.12)]" />
				<span className="text-[0.6875rem] text-[var(--landing-muted)] opacity-50 ml-2">my-saas-app · 5 sessions</span>
			</div>
			<div className="grid grid-cols-4 gap-2 p-3">
				<DashColumn
					title="Working"
					cards={[
						{ title: "Add user auth flow", meta: "#42 · feat/auth", agent: "claude-code" },
						{ title: "Fix pagination bug", meta: "#43 · fix/pagination", agent: "codex" },
					]}
				/>
				<DashColumn title="Pending" cards={[{ title: "Add rate limiting", meta: "#44 · PR #312", agent: "aider" }]} />
				<DashColumn
					title="Review"
					cards={[{ title: "Update API tests", meta: "#45 · PR #310", agent: "claude-code", amber: true }]}
				/>
				<DashColumn
					title="Merged"
					cards={[{ title: "Refactor DB layer", meta: "#46 · PR #308", agent: "opencode", done: true }]}
				/>
			</div>
		</div>
	);
}

function PrsDemo() {
	return (
		<div className="flex flex-col gap-2.5">
			{[
				{ branch: "feat/user-auth", title: "Add user authentication flow" },
				{ branch: "fix/pagination-offset", title: "Fix off-by-one in cursor pagination" },
				{ branch: "feat/rate-limiting", title: "Add Redis-backed rate limiter" },
				{ branch: "refactor/db-layer", title: "Extract repository pattern from services" },
			].map((pr) => (
				<div
					key={pr.branch}
					className="bg-[var(--landing-surface)] border border-[var(--landing-border-subtle)] rounded-xl px-5 py-4 flex items-center justify-between"
				>
					<div className="flex flex-col gap-1">
						<div className="font-mono text-xs text-[var(--landing-fg)]/70">{pr.branch}</div>
						<div className="text-[0.8125rem] text-[var(--landing-muted)]">{pr.title}</div>
					</div>
					<div className="font-mono text-[0.625rem] tracking-[0.05em] px-3 py-1 rounded-full bg-[rgba(134,239,172,0.08)] text-[rgba(134,239,172,0.7)]">
						✓ Merged
					</div>
				</div>
			))}
		</div>
	);
}

interface DashCardData {
	title: string;
	meta: string;
	agent: string;
	amber?: boolean;
	done?: boolean;
}

function DashColumn({ title, cards }: { title: string; cards: DashCardData[] }) {
	return (
		<div>
			<div className="font-mono text-[0.625rem] tracking-[0.1em] uppercase text-[var(--landing-muted)] opacity-40 px-2 mb-1">
				{title}
			</div>
			{cards.map((card) => (
				<div
					key={card.meta}
					className="bg-[var(--landing-surface)] border border-[var(--landing-border-subtle)] rounded-lg p-2.5 mb-1.5 text-[0.6875rem]"
				>
					<div className="text-[var(--landing-fg)]/70 mb-1">{card.title}</div>
					<div className="font-mono text-[0.5625rem] text-[var(--landing-muted)] opacity-50">{card.meta}</div>
					<div className="flex items-center gap-1 mt-1 font-mono text-[0.5625rem] text-[var(--landing-muted)] opacity-60">
						{card.done ? (
							<span>✓</span>
						) : (
							<span
								className="inline-block w-1.5 h-1.5 rounded-full"
								style={{ background: card.amber ? "rgba(251,191,36,0.7)" : "rgba(134,239,172,0.7)" }}
							/>
						)}
						{card.agent}
					</div>
				</div>
			))}
		</div>
	);
}
