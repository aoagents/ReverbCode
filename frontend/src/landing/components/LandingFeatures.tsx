"use client";

import { useEffect, useRef, useState } from "react";

type Asset =
  | { type: "image"; src: string; w: number; h: number }
  | { type: "video"; src: string; poster?: string; w: number; h: number };

// A feature's product surface is either a real capture (image/video dropped
// into /public/features) or an in-frame illustration for concept features
// whose UI isn't shippable as a screenshot yet.
type Surface =
  | { kind: "capture"; asset: Asset }
  | { kind: "render"; id: "parallel" | "recovery" | "slots" };

type Feature = {
  n: string;
  title: string;
  desc: string;
  frameTitle: string;
  cta?: string;
  surface: Surface;
};

const features: Feature[] = [
  {
    n: "01",
    title: "Multi-agent execution",
    desc: "Run Claude Code, Codex, Cursor, Aider, and OpenCode in parallel. Each agent in its own git worktree, branch, and context.",
    frameTitle: "reverbcode · sessions",
    cta: "npx @aoagents/ao start",
    surface: { kind: "render", id: "parallel" },
  },
  {
    n: "02",
    title: "Autonomous CI + review handling",
    desc: "CI fails? The agent reads the logs and pushes a fix. Review comments land? The agent addresses them. You sleep, your agents ship.",
    frameTitle: "reverbcode · s-312",
    surface: { kind: "render", id: "recovery" },
  },
  {
    n: "03",
    title: "Seven swappable slots",
    desc: "Runtime, Agent, Workspace, Tracker, SCM, Notifier, Terminal. Use tmux or process. GitHub or GitLab. Slack or webhooks.",
    frameTitle: "agent-orchestrator.yaml",
    surface: { kind: "render", id: "slots" },
  },
  {
    n: "04",
    title: "Real-time Kanban + terminal",
    desc: "Every agent's state in one view. Attach to any terminal via the browser. SSE updates every 5 seconds. WebSocket for live I/O.",
    frameTitle: "reverbcode · live",
    surface: {
      kind: "capture",
      asset: { type: "video", src: "/features/live.webm", poster: "/features/live.png", w: 1280, h: 800 },
    },
  },
];

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
    <section className="py-[96px] px-6 md:px-16 max-w-[78rem] mx-auto" id="features">
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
        {features.map((f, i) => {
          const flip = i % 2 === 1; // alternate product/text sides down the deck
          return (
            <div
              key={f.n}
              ref={(el) => {
                cardRefs.current[i] = el;
              }}
              className="landing-card rounded-2xl overflow-hidden"
              style={{
                padding: "clamp(1.5rem, 3vw, 2.5rem)",
                marginBottom: "1.5rem",
                transformOrigin: "center top",
                transition: "transform 0.4s ease, opacity 0.4s ease, border-color 0.2s ease",
                ...(stack
                  ? { position: "sticky", top: `${BASE_TOP + i * STACK_GAP}px`, zIndex: i + 1 }
                  : null),
              }}
            >
              <div className="grid grid-cols-1 md:grid-cols-2 gap-8 md:gap-12 items-center">
                <div className={flip ? "md:order-2" : ""}>
                  <div className="font-mono text-xs tracking-[0.1em] text-[var(--landing-muted)] opacity-50 mb-4">
                    {f.n}
                  </div>
                  <h3 className="font-sans font-[680] tracking-tight text-[1.375rem] mb-4">
                    {f.title}
                  </h3>
                  <p className="text-[var(--landing-muted)] text-[0.9375rem] leading-[1.7] max-w-[28rem]">
                    {f.desc}
                  </p>
                  {f.cta && (
                    <div className="mt-6">
                      <CopyCommand text={f.cta} />
                    </div>
                  )}
                </div>
                <div className={flip ? "md:order-1" : ""}>
                  <ProductFrame feature={f} />
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

// macOS-style window chrome around the product surface — the universal "this is
// real software" cue. Same chrome for captures and illustrations so all four
// cards read as one coherent product.
function ProductFrame({ feature }: { feature: Feature }) {
  const s = feature.surface;
  const ratio = s.kind === "capture" ? `${s.asset.w} / ${s.asset.h}` : "1280 / 800";
  return (
    <div className="rounded-xl border border-[var(--landing-border-default)] bg-[#171614] overflow-hidden shadow-2xl shadow-black/40">
      <div className="flex items-center gap-2 px-4 h-9 border-b border-[var(--landing-border-subtle)] bg-[rgba(255,255,255,0.022)]">
        <span className="flex gap-1.5 shrink-0">
          <span className="w-3 h-3 rounded-full bg-[#ff5f57]/75" />
          <span className="w-3 h-3 rounded-full bg-[#febc2e]/75" />
          <span className="w-3 h-3 rounded-full bg-[#28c840]/75" />
        </span>
        <span className="mx-auto font-mono text-[0.6875rem] text-[var(--landing-muted)] truncate">
          {feature.frameTitle}
        </span>
        <span className="w-[52px] shrink-0" aria-hidden="true" />
      </div>
      <div
        className="relative bg-[#111010]"
        style={{ aspectRatio: ratio }}
      >
        {s.kind === "capture" ? (
          <ProductMedia asset={s.asset} title={feature.title} />
        ) : s.id === "parallel" ? (
          <ParallelSurface />
        ) : s.id === "recovery" ? (
          <RecoverySurface />
        ) : (
          <SlotsSurface />
        )}
      </div>
    </div>
  );
}

// Renders the real asset; falls back to a labelled placeholder if it 404s, so
// dropping a file into /public/features just works with no code change.
function ProductMedia({ asset, title }: { asset: Asset; title: string }) {
  const [failed, setFailed] = useState(false);

  if (failed) {
    const name = asset.src.split("/").pop();
    return (
      <div className="landing-shimmer absolute inset-0 flex flex-col items-center justify-center gap-1.5 overflow-hidden">
        <div className="font-mono text-[0.6875rem] text-[var(--landing-muted)]">{name}</div>
        <div className="font-mono text-[0.625rem] text-[var(--landing-muted-dim)] opacity-60">
          {asset.w}×{asset.h}
          {asset.type === "video" ? " · loop" : ""}
        </div>
      </div>
    );
  }

  if (asset.type === "video") {
    return (
      <video
        className="absolute inset-0 w-full h-full object-cover"
        src={asset.src}
        poster={asset.poster}
        autoPlay
        muted
        loop
        playsInline
        onError={() => setFailed(true)}
      />
    );
  }

  return (
    // eslint-disable-next-line @next/next/no-img-element
    <img
      className="absolute inset-0 w-full h-full object-cover"
      src={asset.src}
      alt={`${title} — ReverbCode`}
      onError={() => setFailed(true)}
    />
  );
}

// Click-to-copy install command — primary conversion action, kept beside the
// first feature where the eye already is.
function CopyCommand({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={() => {
        navigator.clipboard?.writeText(text).then(() => {
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1600);
        });
      }}
      className="inline-flex items-center gap-2.5 rounded-lg border border-[var(--landing-border-default)] bg-[rgba(255,240,220,0.03)] px-3.5 py-2 font-mono text-[0.8125rem] text-[var(--landing-fg)]/85 hover:border-[var(--landing-border-strong)] transition-colors"
    >
      <span className="text-[var(--landing-muted-dim)]">$</span>
      <span>{text}</span>
      <span className="text-[var(--landing-muted-dim)] text-[0.6875rem]">
        {copied ? "copied" : "copy"}
      </span>
    </button>
  );
}

/* ──────── 01 · Parallel sessions (illustration) ──────── */

const parallelAgents = [
  { name: "claude-code", task: "#42 auth", color: "rgba(255,159,102,0.7)", dur: 3.4, delay: 0 },
  { name: "codex", task: "#43 pagination", color: "rgba(134,239,172,0.65)", dur: 4.2, delay: 0.5 },
  { name: "aider", task: "#44 rate limit", color: "rgba(167,139,250,0.65)", dur: 3.6, delay: 1.0 },
  { name: "opencode", task: "#46 db refactor", color: "rgba(96,165,250,0.65)", dur: 4.8, delay: 0.3 },
];

function ParallelSurface() {
  return (
    <div className="absolute inset-0 p-6 flex flex-col">
      <div className="flex items-center justify-between mb-4">
        <span className="font-mono text-[0.6875rem] tracking-[0.12em] uppercase text-[var(--landing-muted-dim)]">
          4 sessions · parallel
        </span>
        <span className="font-mono text-[0.625rem] text-[var(--landing-muted-dim)] flex items-center gap-1.5">
          <span className="w-1.5 h-1.5 rounded-full bg-[rgba(134,239,172,0.7)] landing-sse-pulse" />
          live
        </span>
      </div>
      <div className="grid grid-cols-2 gap-3 flex-1">
        {parallelAgents.map((a) => (
          <div
            key={a.name}
            className="bg-[rgba(255,240,220,0.035)] border border-[var(--landing-border-subtle)] rounded-xl p-3.5 flex flex-col"
          >
            <div className="flex items-center gap-2 mb-1.5">
              <span className="w-2 h-2 rounded-full shrink-0" style={{ background: a.color }} />
              <span className="font-mono text-[0.75rem] text-[var(--landing-fg)]/85 truncate">
                {a.name}
              </span>
            </div>
            <div className="font-mono text-[0.6875rem] text-[var(--landing-muted)] opacity-65 mb-auto truncate">
              {a.task}
            </div>
            <div className="h-[3px] rounded-full bg-[var(--landing-border-subtle)] overflow-hidden mt-3">
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

/* ──────── 02 · Autonomous recovery (illustration) ──────── */

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

function RecoverySurface() {
  const [count, setCount] = useState(4);
  useEffect(() => {
    const id = setInterval(() => {
      setCount((c) => (c >= recoveryStages.length ? 4 : c + 1));
    }, 950);
    return () => clearInterval(id);
  }, []);
  const visible = recoveryStages.slice(0, count);
  return (
    <div className="absolute inset-0 p-6 flex flex-col font-mono text-[0.8125rem]">
      <div className="flex items-center justify-between mb-4 pb-4 border-b border-[var(--landing-border-subtle)]">
        <span className="text-[var(--landing-fg)]/80">PR #312 · feat/user-auth</span>
        <span className="text-[0.625rem] uppercase tracking-[0.12em] text-[var(--landing-muted-dim)]">
          healing
        </span>
      </div>
      <div className="flex-1 space-y-2.5 overflow-hidden">
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
              className={`flex items-baseline gap-3 ${isLast ? "landing-stream-line" : ""}`}
            >
              <span className="text-[var(--landing-muted-dim)] opacity-50 w-10 shrink-0">
                {s.time}
              </span>
              <span className={`${color} truncate`}>{s.text}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

/* ──────── 03 · Swappable slots (illustration) ──────── */

const slots: { slot: string; values: string[] }[] = [
  { slot: "agent", values: ["claude-code", "codex", "aider", "opencode"] },
  { slot: "tracker", values: ["github", "linear", "gitlab"] },
  { slot: "runtime", values: ["tmux", "process"] },
  { slot: "workspace", values: ["worktree", "clone"] },
  { slot: "scm", values: ["github", "gitlab"] },
  { slot: "notifier", values: ["slack", "webhook", "desktop"] },
  { slot: "terminal", values: ["iterm2", "web"] },
];

function SlotsSurface() {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), 1600);
    return () => clearInterval(id);
  }, []);
  return (
    <div className="absolute inset-0 p-6 flex flex-col">
      <div className="flex items-center justify-between mb-4 pb-4 border-b border-[var(--landing-border-subtle)]">
        <span className="font-mono text-[0.8125rem] text-[var(--landing-fg)]/80">
          agent-orchestrator.yaml
        </span>
        <span className="font-mono text-[0.625rem] tracking-[0.12em] uppercase text-[var(--landing-muted-dim)]">
          7 slots
        </span>
      </div>
      <div className="flex flex-col gap-2.5 font-mono text-[0.8125rem]">
        {slots.map((s, i) => {
          const val = s.values[(tick + i) % s.values.length];
          return (
            <div key={s.slot} className="flex items-center gap-3">
              <span className="text-[var(--landing-muted-dim)] w-[5.5rem] shrink-0">{s.slot}:</span>
              <span
                key={val}
                className="landing-chip-swap inline-block px-2.5 py-[2px] rounded-md bg-[rgba(255,240,220,0.05)] text-[var(--landing-fg)]/85 border border-[var(--landing-border-subtle)]"
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
