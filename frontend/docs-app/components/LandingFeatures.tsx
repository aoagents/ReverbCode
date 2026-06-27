"use client";

import { useEffect, useRef, useState } from "react";

type DemoKind = "parallel" | "recovery" | "plugins" | "dashboard";

const features: { n: string; title: string; desc: string; demo: DemoKind }[] = [
	{
		n: "01",
		title: "Multi-agent execution",
		desc: "Run Claude Code, Codex, Cursor, Aider, and OpenCode in parallel. Each agent in its own git worktree, branch, and context.",
		demo: "parallel",
	},
	{
		n: "02",
		title: "Autonomous CI + review handling",
		desc: "CI fails? The agent reads the logs and pushes a fix. Review comments land? The agent addresses them. You sleep, your agents ship.",
		demo: "recovery",
	},
	{
		n: "03",
		title: "Seven swappable slots",
		desc: "Runtime, Agent, Workspace, Tracker, SCM, Notifier, Terminal. Use tmux or process. GitHub or GitLab. Slack or webhooks.",
		demo: "plugins",
	},
	{
		n: "04",
		title: "Real-time Kanban + terminal",
		desc: "Every agent's state in one view. Attach to any terminal via the browser. SSE updates every 5 seconds. WebSocket for live I/O.",
		demo: "dashboard",
	},
];

// The feature's animated demo — the stacked back panel + a smaller front peek,
// reused as-is from the original switcher so each card stays rich.
function FeatureDemo({ kind }: { kind: DemoKind }) {
	return (
		<div className="relative h-[460px]">
			<div className="absolute top-0 right-0 w-[90%] h-[390px] bg-[rgba(255,240,220,0.04)] rounded-2xl border border-[var(--landing-border-subtle)] overflow-hidden landing-feat-card">
				{kind === "parallel" && <ParallelBack />}
				{kind === "recovery" && <RecoveryBack />}
				{kind === "plugins" && <PluginsBack />}
				{kind === "dashboard" && <DashboardBack />}
			</div>
			<div className="absolute bottom-0 left-0 w-[58%] h-[230px] bg-[rgba(255,240,220,0.06)] rounded-2xl border border-[var(--landing-border-default)] overflow-hidden landing-feat-card-front">
				{kind === "parallel" && <ParallelFront />}
				{kind === "recovery" && <RecoveryFront />}
				{kind === "plugins" && <PluginsFront />}
				{kind === "dashboard" && <DashboardFront />}
			</div>
		</div>
	);
}

// Sticky offset from the top of the viewport where each card pins (leaves room
// for the fixed nav); each successive card pins STACK_GAP lower so the tops peek.
const BASE_TOP = 120;
const STACK_GAP = 26;

export function LandingFeatures() {
	const cardRefs = useRef<(HTMLDivElement | null)[]>([]);
	const [stack, setStack] = useState(false);

	// Scroll-stack only on desktop; on narrow screens cards read as a plain list.
	useEffect(() => {
		const mq = window.matchMedia("(min-width: 768px)");
		const apply = () => setStack(mq.matches);
		apply();
		mq.addEventListener("change", apply);
		return () => mq.removeEventListener("change", apply);
	}, []);

	// As later cards pin on top, shrink + dim the cards beneath them so the deck
	// reads as a stack. CSS transition smooths the steps; rAF throttles scroll.
	useEffect(() => {
		const els = cardRefs.current;
		if (!stack) {
			els.forEach((el) => {
				if (el) {
					el.style.transform = "";
					el.style.opacity = "";
				}
			});
			return;
		}
		let raf = 0;
		const update = () => {
			raf = 0;
			els.forEach((el, i) => {
				if (!el) return;
				let depth = 0;
				for (let j = i + 1; j < els.length; j++) {
					const ej = els[j];
					if (ej && ej.getBoundingClientRect().top <= BASE_TOP + j * STACK_GAP + 0.5) {
						depth += 1;
					}
				}
				el.style.transform = `scale(${1 - depth * 0.05})`;
				el.style.opacity = `${Math.max(1 - depth * 0.16, 0.55)}`;
			});
		};
		const onScroll = () => {
			if (!raf) raf = requestAnimationFrame(update);
		};
		update();
		window.addEventListener("scroll", onScroll, { passive: true });
		window.addEventListener("resize", onScroll);
		return () => {
			window.removeEventListener("scroll", onScroll);
			window.removeEventListener("resize", onScroll);
			if (raf) cancelAnimationFrame(raf);
		};
	}, [stack]);

	return (
		<section className="py-[96px] px-6 md:px-16 max-w-[68rem] mx-auto" id="features">
			<div className="landing-reveal text-center">
				<span className="inline-block border border-[var(--landing-border-strong)] rounded-full px-4 py-[5px] text-[13px] text-[var(--landing-muted)] mb-5">
					Features
				</span>
			</div>

			<h2
				className="landing-reveal text-center mx-auto mb-[72px] max-w-[36rem] text-[var(--landing-fg)]"
				style={{
					fontFamily: "var(--font-instrument-serif), ui-serif, Georgia, serif",
					fontSize: "clamp(2.25rem, 5vw, 3.5rem)",
					lineHeight: 1.08,
					fontWeight: 400,
				}}
			>
				A unified orchestrator <em className="italic text-[var(--landing-muted)]">that scales.</em>
			</h2>

			<div className="relative">
				{features.map((f, i) => (
					<div
						key={f.n}
						ref={(el) => {
							cardRefs.current[i] = el;
						}}
						className="landing-card rounded-2xl grid grid-cols-1 md:grid-cols-2 gap-8 md:gap-12 items-center overflow-hidden"
						style={{
							padding: "clamp(1.5rem, 3vw, 2.5rem)",
							marginBottom: "1.5rem",
							transformOrigin: "center top",
							transition: "transform 0.4s ease, opacity 0.4s ease, border-color 0.2s ease",
							...(stack ? { position: "sticky", top: `${BASE_TOP + i * STACK_GAP}px`, zIndex: i + 1 } : null),
						}}
					>
						<div>
							<div className="font-mono text-xs tracking-[0.1em] text-[var(--landing-muted)] opacity-50 mb-4">
								{f.n}
							</div>
							<h3 className="font-sans font-[680] tracking-tight text-[1.375rem] mb-4">{f.title}</h3>
							<p className="text-[var(--landing-muted)] text-[0.9375rem] leading-[1.7] max-w-[28rem]">{f.desc}</p>
						</div>
						<FeatureDemo kind={f.demo} />
					</div>
				))}
			</div>
		</section>
	);
}

/* ──────── 01 · Parallel ──────── */

function ParallelBack() {
	const agents = [
		{ name: "claude-code", task: "#42 auth", color: "rgba(255,159,102,0.7)", dur: 3.4, delay: 0 },
		{ name: "codex", task: "#43 pagination", color: "rgba(134,239,172,0.65)", dur: 4.2, delay: 0.5 },
		{ name: "aider", task: "#44 rate limit", color: "rgba(167,139,250,0.65)", dur: 3.6, delay: 1.0 },
		{ name: "opencode", task: "#46 db refactor", color: "rgba(96,165,250,0.65)", dur: 4.8, delay: 0.3 },
	];
	return (
		<div className="p-5 h-full flex flex-col">
			<div className="flex items-center justify-between mb-4">
				<span className="font-mono text-[0.625rem] tracking-[0.12em] uppercase text-[var(--landing-muted-dim)]">
					4 sessions · parallel
				</span>
				<span className="font-mono text-[0.5625rem] text-[var(--landing-muted-dim)] flex items-center gap-1.5">
					<span className="w-1 h-1 rounded-full bg-[rgba(134,239,172,0.7)] landing-sse-pulse" />
					live
				</span>
			</div>
			<div className="grid grid-cols-2 gap-2.5 flex-1">
				{agents.map((a) => (
					<div
						key={a.name}
						className="bg-[rgba(255,240,220,0.035)] border border-[var(--landing-border-subtle)] rounded-xl p-3 flex flex-col"
					>
						<div className="flex items-center gap-1.5 mb-1">
							<span className="w-1.5 h-1.5 rounded-full shrink-0" style={{ background: a.color }} />
							<span className="font-mono text-[0.6875rem] text-[var(--landing-fg)]/85 truncate">{a.name}</span>
						</div>
						<div className="font-mono text-[0.625rem] text-[var(--landing-muted)] opacity-65 mb-auto truncate">
							{a.task}
						</div>
						<div className="h-[3px] rounded-full bg-[var(--landing-border-subtle)] overflow-hidden mt-2">
							<div
								className="h-full landing-feature-bar"
								style={{
									background: a.color,
									animationDuration: `${a.dur}s`,
									animationDelay: `${a.delay}s`,
								}}
							/>
						</div>
					</div>
				))}
			</div>
		</div>
	);
}

function ParallelFront() {
	const fleet = [
		{ name: "claude-code", color: "rgba(255,159,102,0.85)" },
		{ name: "codex", color: "rgba(134,239,172,0.75)" },
		{ name: "aider", color: "rgba(167,139,250,0.75)" },
		{ name: "opencode", color: "rgba(96,165,250,0.75)" },
		{ name: "cursor", color: "rgba(244,114,182,0.65)" },
	];
	return (
		<div className="flex flex-col p-5 w-full">
			<div className="font-mono text-[0.5625rem] tracking-[0.12em] uppercase text-[var(--landing-muted-dim)] mb-2.5">
				Fleet · 5 agents
			</div>
			<div className="flex flex-col gap-1.5">
				{fleet.map((a) => (
					<div
						key={a.name}
						className="flex items-center gap-2 bg-[rgba(255,240,220,0.04)] border border-[var(--landing-border-subtle)] rounded-md px-2.5 py-1.5"
					>
						<span className="w-1.5 h-1.5 rounded-full shrink-0" style={{ background: a.color }} />
						<span className="font-mono text-[0.6875rem] text-[var(--landing-fg)]/85 truncate">{a.name}</span>
					</div>
				))}
			</div>
		</div>
	);
}

/* ──────── 02 · Recovery ──────── */

const recoveryStages: { time: string; text: string; kind: "info" | "fail" | "fix" | "ok" }[] = [
	{ time: "10:42", text: "agent.spawn → s-312", kind: "info" },
	{ time: "10:43", text: "✗ tests/auth failed", kind: "fail" },
	{ time: "10:44", text: "agent.investigate()", kind: "info" },
	{ time: "10:44", text: "patch · re-running ci", kind: "fix" },
	{ time: "10:45", text: "✓ tests/auth (48/48)", kind: "ok" },
	{ time: "10:45", text: "✗ lint failed", kind: "fail" },
	{ time: "10:46", text: "patch · eslint --fix", kind: "fix" },
	{ time: "10:47", text: "✓ lint passed", kind: "ok" },
	{ time: "10:47", text: "● ready to merge", kind: "ok" },
];

function RecoveryBack() {
	const [count, setCount] = useState(3);
	useEffect(() => {
		const id = setInterval(() => {
			setCount((c) => (c >= recoveryStages.length ? 3 : c + 1));
		}, 1000);
		return () => clearInterval(id);
	}, []);
	const visible = recoveryStages.slice(0, count);
	return (
		<div className="p-5 h-full flex flex-col font-mono text-[0.6875rem]">
			<div className="flex items-center justify-between mb-3 pb-3 border-b border-[var(--landing-border-subtle)]">
				<span className="text-[var(--landing-fg)]/80">PR #312 · feat/user-auth</span>
				<span className="text-[0.5625rem] uppercase tracking-[0.1em] text-[var(--landing-muted-dim)]">healing</span>
			</div>
			<div className="flex-1 space-y-1.5 overflow-hidden">
				{visible.map((s, i) => {
					const isLast = i === visible.length - 1;
					const color =
						s.kind === "fail"
							? "text-[rgba(248,113,113,0.85)]"
							: s.kind === "ok"
								? "text-[rgba(134,239,172,0.85)]"
								: s.kind === "fix"
									? "text-[rgba(251,191,36,0.85)]"
									: "text-[var(--landing-muted)]";
					return (
						<div
							key={`${i}-${s.text}`}
							className={`flex items-baseline gap-2.5 ${isLast ? "landing-stream-line" : ""}`}
						>
							<span className="text-[var(--landing-muted-dim)] opacity-50 w-9 shrink-0">{s.time}</span>
							<span className={`${color} truncate`}>{s.text}</span>
						</div>
					);
				})}
			</div>
		</div>
	);
}

function RecoveryFront() {
	return (
		<div className="grid grid-cols-2 gap-2.5 p-5 w-full h-full items-stretch">
			<div className="bg-[rgba(248,113,113,0.05)] border border-[rgba(248,113,113,0.18)] rounded-xl p-3 flex flex-col items-center justify-center gap-1">
				<span className="font-mono text-[0.5rem] tracking-[0.12em] uppercase text-[rgba(248,113,113,0.7)]">before</span>
				<span className="text-[1.75rem] leading-none text-[rgba(248,113,113,0.85)]">✗</span>
				<span className="font-mono text-[0.625rem] text-[var(--landing-fg)]/70">12/48</span>
			</div>
			<div className="bg-[rgba(134,239,172,0.05)] border border-[rgba(134,239,172,0.2)] rounded-xl p-3 flex flex-col items-center justify-center gap-1">
				<span className="font-mono text-[0.5rem] tracking-[0.12em] uppercase text-[rgba(134,239,172,0.7)]">after</span>
				<span className="text-[1.75rem] leading-none text-[rgba(134,239,172,0.85)]">✓</span>
				<span className="font-mono text-[0.625rem] text-[var(--landing-fg)]/70">48/48</span>
			</div>
		</div>
	);
}

/* ──────── 03 · Plugins ──────── */

function PluginsBack() {
	const slots = [
		{ slot: "agent", values: ["claude-code", "codex", "aider", "opencode"] },
		{ slot: "tracker", values: ["github", "linear", "gitlab"] },
		{ slot: "runtime", values: ["tmux", "process"] },
		{ slot: "workspace", values: ["worktree", "clone"] },
		{ slot: "scm", values: ["github", "gitlab"] },
		{ slot: "notifier", values: ["slack", "webhook", "desktop"] },
		{ slot: "terminal", values: ["iterm2", "web"] },
	];
	const [tick, setTick] = useState(0);
	useEffect(() => {
		const id = setInterval(() => setTick((t) => t + 1), 1600);
		return () => clearInterval(id);
	}, []);
	return (
		<div className="p-5 h-full flex flex-col">
			<div className="flex items-center justify-between mb-3 pb-3 border-b border-[var(--landing-border-subtle)]">
				<span className="font-mono text-[0.6875rem] text-[var(--landing-fg)]/80">agent-orchestrator.yaml</span>
				<span className="font-mono text-[0.5625rem] tracking-[0.1em] uppercase text-[var(--landing-muted-dim)]">
					7 slots
				</span>
			</div>
			<div className="flex flex-col gap-1.5 font-mono text-[0.6875rem]">
				{slots.map((s, i) => {
					const val = s.values[(tick + i) % s.values.length];
					return (
						<div key={s.slot} className="flex items-center gap-3">
							<span className="text-[var(--landing-muted-dim)] w-[4.5rem] shrink-0">{s.slot}:</span>
							<span
								key={val}
								className="landing-chip-swap inline-block px-2 py-[1px] rounded-md bg-[rgba(255,240,220,0.05)] text-[var(--landing-fg)]/85 border border-[var(--landing-border-subtle)]"
							>
								{val}
							</span>
						</div>
					);
				})}
			</div>
		</div>
	);
}

function PluginsFront() {
	const pairs = [
		{ from: "tmux", to: "process" },
		{ from: "github", to: "linear" },
		{ from: "slack", to: "webhook" },
		{ from: "worktree", to: "clone" },
	];
	const [idx, setIdx] = useState(0);
	useEffect(() => {
		const id = setInterval(() => setIdx((i) => (i + 1) % pairs.length), 1800);
		return () => clearInterval(id);
	}, []);
	const p = pairs[idx];
	return (
		<div className="flex flex-col items-center justify-center gap-3 p-5 w-full h-full">
			<span className="font-mono text-[0.5625rem] tracking-[0.12em] uppercase text-[var(--landing-muted-dim)]">
				swap
			</span>
			<div className="flex items-center gap-3">
				<span
					key={`from-${idx}`}
					className="landing-chip-swap font-mono text-[0.8125rem] px-2.5 py-1 rounded-md bg-[rgba(255,240,220,0.05)] text-[var(--landing-fg)]/85 border border-[var(--landing-border-subtle)]"
				>
					{p.from}
				</span>
				<span className="text-[var(--landing-muted)] text-base">⇄</span>
				<span
					key={`to-${idx}`}
					className="landing-chip-swap font-mono text-[0.8125rem] px-2.5 py-1 rounded-md bg-[rgba(255,240,220,0.08)] text-[var(--landing-fg)]/90 border border-[var(--landing-border-default)]"
				>
					{p.to}
				</span>
			</div>
		</div>
	);
}

/* ──────── 04 · Dashboard ──────── */

type KanbanCard = {
	id: number;
	col: 0 | 1 | 2;
	title: string;
	agent: string;
	color: string;
};

function DashboardBack() {
	const [cards, setCards] = useState<KanbanCard[]>([
		{ id: 1, col: 0, title: "Add user auth", agent: "claude-code", color: "rgba(255,159,102,0.7)" },
		{ id: 2, col: 0, title: "Fix pagination", agent: "codex", color: "rgba(134,239,172,0.65)" },
		{ id: 3, col: 1, title: "Add rate limit", agent: "aider", color: "rgba(167,139,250,0.65)" },
		{ id: 4, col: 2, title: "Refactor DB", agent: "opencode", color: "rgba(96,165,250,0.65)" },
	]);
	useEffect(() => {
		const id = setInterval(() => {
			setCards((prev) => {
				const advanceable = prev.filter((c) => c.col < 2);
				if (advanceable.length === 0) {
					return prev.map((c) => ({ ...c, col: 0 as 0 | 1 | 2 }));
				}
				const oldest = advanceable[0];
				return prev.map((c) => (c.id === oldest.id ? { ...c, col: (c.col + 1) as 0 | 1 | 2 } : c));
			});
		}, 2400);
		return () => clearInterval(id);
	}, []);
	const cols = ["Working", "Review", "Merged"];
	return (
		<div className="p-5 h-full flex flex-col">
			<div className="flex items-center justify-between mb-3 pb-3 border-b border-[var(--landing-border-subtle)]">
				<span className="font-mono text-[0.6875rem] text-[var(--landing-fg)]/80">my-saas-app · 4 sessions</span>
				<span className="font-mono text-[0.5625rem] tracking-[0.1em] uppercase text-[var(--landing-muted-dim)] flex items-center gap-1.5">
					<span className="w-1 h-1 rounded-full bg-[rgba(134,239,172,0.7)] landing-sse-pulse" />
					sse
				</span>
			</div>
			<div className="grid grid-cols-3 gap-2 flex-1">
				{cols.map((name, col) => (
					<div key={name} className="space-y-1.5">
						<div className="font-mono text-[0.5rem] tracking-[0.12em] uppercase text-[var(--landing-muted-dim)] mb-1.5">
							{name}
						</div>
						{cards
							.filter((c) => c.col === col)
							.map((c) => (
								<div
									key={c.id}
									className="bg-[rgba(255,240,220,0.035)] border border-[var(--landing-border-subtle)] rounded-lg p-2 text-[0.625rem] leading-tight landing-stream-line"
								>
									<div className="text-[var(--landing-fg)]/85 truncate mb-1">{c.title}</div>
									<div className="flex items-center gap-1">
										<span className="w-1 h-1 rounded-full shrink-0" style={{ background: c.color }} />
										<span className="font-mono text-[0.5rem] text-[var(--landing-muted-dim)] truncate">{c.agent}</span>
									</div>
								</div>
							))}
					</div>
				))}
			</div>
		</div>
	);
}

const streamPool = [
	"tests/auth.py::test_login",
	"tests/api.py::test_pagination",
	"tests/db.py::test_migration",
	"tests/queue.py::test_dequeue",
	"tests/auth.py::test_logout",
	"tests/api.py::test_cursor",
	"tests/db.py::test_index",
	"tests/queue.py::test_retry",
];

function DashboardFront() {
	const [stream, setStream] = useState(() =>
		streamPool.slice(0, 4).map((text, i) => ({ id: i, text, exiting: false })),
	);
	const nextRef = useRef(4);
	useEffect(() => {
		const id = setInterval(() => {
			setStream((prev) => {
				const marked = prev.map((l, i) => (i === 0 ? { ...l, exiting: true } : l));
				const next = [
					...marked,
					{
						id: nextRef.current,
						text: streamPool[nextRef.current % streamPool.length],
						exiting: false,
					},
				];
				nextRef.current += 1;
				return next;
			});
			setTimeout(() => {
				setStream((prev) => prev.filter((l) => !l.exiting));
			}, 240);
		}, 1300);
		return () => clearInterval(id);
	}, []);
	return (
		<div className="p-4 w-full h-full font-mono flex flex-col">
			<div className="flex items-center justify-between mb-2 pb-1.5 border-b border-[var(--landing-border-subtle)]">
				<span className="text-[0.6875rem] text-[var(--landing-fg)]/80">s-003 · attached</span>
				<span className="text-[0.5rem] text-[var(--landing-muted-dim)]">tail -f</span>
			</div>
			<div className="space-y-[2px] text-[0.625rem] text-[var(--landing-muted)] leading-[1.6] flex-1 overflow-hidden">
				{stream.map((l) => (
					<div
						key={l.id}
						className={`truncate transition-opacity duration-200 ${l.exiting ? "opacity-0" : "landing-stream-line"}`}
					>
						<span className="text-[rgba(134,239,172,0.7)]">✓</span> <span className="opacity-70">{l.text}</span>
					</div>
				))}
			</div>
		</div>
	);
}
