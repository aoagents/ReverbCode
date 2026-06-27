import React, { useEffect, useState } from "react";
import { ArrowRight, Github, BookOpen, Star, GitFork, Users } from "lucide-react";
import { motion } from "framer-motion";

const scripts = [
  {
    agent: "codex",
    branch: "wt-refactor-auth",
    dot: "var(--status-ok)",
    lines: [
      "▸ analyzing src/auth/*.go",
      "▸ planning JWT migration · 8 files",
      "✎ wrote handler.go (+142 / -56)",
      "✎ wrote middleware.go (+78 / -12)",
      "$ go test ./auth/...",
      "✓ ok  43 tests · 0.41s",
      "$ git push -u origin add-jwt",
      "PR #482 opened",
    ],
  },
  {
    agent: "claude-code",
    branch: "wt-add-sso-okta",
    dot: "var(--accent)",
    lines: [
      "▸ reading docs/auth-flows.md",
      "▸ drafting okta provider adapter",
      "✎ wrote sso/okta.go (+318)",
      "$ go vet ./...",
      "⚠ lint: unused import",
      "✎ fixed lint",
      "$ go test -race ./sso/...",
      "✓ ok  21 tests · 1.04s",
    ],
  },
  {
    agent: "cursor",
    branch: "wt-stripe-webhooks",
    dot: "var(--status-warn)",
    lines: [
      "▸ scaffolding /webhooks/stripe",
      "▸ signing verifier · idempotency",
      "✎ wrote webhooks.go (+184)",
      "$ go test -run TestStripe",
      "✗ 1 failed: signature mismatch",
      "[scm] CI red → nudge to sess",
      "✎ patched verifier",
      "✓ all green · 12 tests",
    ],
  },
  {
    agent: "aider",
    branch: "wt-docs-rewrite",
    dot: "var(--status-review)",
    lines: [
      "▸ scanning README.md + /docs",
      "▸ unifying voice + structure",
      "✎ rewrote README (+612 / -380)",
      "✎ added quickstart.mdx",
      "✎ added architecture diagram",
      "$ markdownlint .",
      "✓ no issues",
      "PR #1077 opened",
    ],
  },
];

function AgentPane({ script, index }) {
  const [lineIdx, setLineIdx] = useState(0);

  useEffect(() => {
    let mounted = true;
    let timer;
    const start = setTimeout(() => {
      let i = 0;
      const tick = () => {
        if (!mounted) return;
        i = (i + 1) % (script.lines.length + 1);
        setLineIdx(i);
        timer = setTimeout(tick, 700 + Math.random() * 500);
      };
      timer = setTimeout(tick, 600);
    }, index * 350);
    return () => {
      mounted = false;
      clearTimeout(start);
      clearTimeout(timer);
    };
  }, [index, script.lines]);

  const visible = script.lines.slice(0, lineIdx);

  const colorFor = (l) => {
    if (l.startsWith("✓")) return "var(--status-ok)";
    if (l.startsWith("✗") || l.startsWith("⚠")) return "var(--status-fail)";
    if (l.startsWith("$")) return "var(--fg)";
    if (l.startsWith("[")) return "var(--fg-muted)";
    if (l.includes("PR #")) return "var(--accent)";
    return "var(--fg-muted)";
  };

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.4, delay: 0.15 + index * 0.08 }}
      data-testid={`fleet-pane-${index}`}
      className="bg-[color:var(--code-bg)] border border-[color:var(--border)] rounded-md overflow-hidden h-full min-h-[178px]"
    >
      <div className="flex items-center gap-2 px-3 py-2 border-b border-[color:var(--border)] bg-[color:var(--code-chrome)]">
        <span
          className="w-1.5 h-1.5 rounded-full pulse-dot"
          style={{ background: script.dot }}
        />
        <span className="font-mono text-[10px] tracking-wide text-[color:var(--code-muted)]">
          {script.agent}
        </span>
        <span className="font-mono text-[9px] text-[color:var(--code-muted)] ml-auto truncate">
          {script.branch}
        </span>
      </div>
      <div className="p-2.5 font-mono text-[10.5px] leading-[1.55]">
        {visible.map((l, i) => (
          <div
            key={i}
            className="whitespace-nowrap overflow-hidden text-ellipsis"
            style={{ color: colorFor(l) }}
          >
            {l}
          </div>
        ))}
        {lineIdx < script.lines.length && <span className="caret" />}
      </div>
    </motion.div>
  );
}

export default function Hero() {
  return (
    <section
      data-testid="hero-section"
      id="top"
      className="relative pt-14 sm:pt-20 pb-16 sm:pb-24 overflow-hidden"
    >
      {/* ambient glow */}
      <div
        className="pointer-events-none absolute top-0 left-1/2 -translate-x-1/2 w-[900px] h-[500px] opacity-50"
        style={{
          background:
            "radial-gradient(ellipse at center, rgba(77,141,255,0.18), transparent 60%)",
        }}
      />

      <div className="container-page relative z-10">
        {/* small label */}
        <motion.div
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.5 }}
          className="hidden"
          data-testid="hero-badge"
        />

        <div className="grid lg:grid-cols-12 gap-10 lg:gap-10 items-stretch">
          {/* LEFT - copy */}
          <div className="lg:col-span-6 flex flex-col justify-center">
            <motion.h1
              initial={{ opacity: 0, y: 16 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6 }}
              data-testid="hero-headline"
              className="font-display font-bold leading-[1.02] tracking-tight text-[color:var(--fg)]"
              style={{ fontSize: "clamp(40px, 6.4vw, 88px)" }}
            >
              Run a fleet of{" "}
              <span className="font-editorial italic font-medium text-[color:var(--accent)]">
                coding agents.
              </span>
              <br />
              <span className="text-[color:var(--fg-muted)]">In parallel. Without the chaos.</span>
            </motion.h1>

            <motion.p
              initial={{ opacity: 0, y: 12 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6, delay: 0.15 }}
              data-testid="hero-subtitle"
              className="mt-7 max-w-xl text-[16px] sm:text-[17px] leading-[1.55] text-[color:var(--fg-muted)]"
            >
              One Go daemon. Twenty-three agent harnesses. Per-session{" "}
              <span className="text-[color:var(--fg)] font-mono text-[15px]">git worktrees</span>.
              The orchestrator watches your PRs and routes CI failures, merge conflicts and
              review comments back to the agent that owns them - automatically.
            </motion.p>

            <motion.div
              initial={{ opacity: 0, y: 12 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6, delay: 0.3 }}
              className="mt-9 flex flex-wrap items-center gap-3"
            >
              <a
                href="https://github.com/AgentWrapper/agent-orchestrator"
                target="_blank"
                rel="noreferrer"
                data-testid="hero-primary-cta"
                className="group inline-flex items-center gap-2 bg-[color:var(--accent)] text-white font-semibold text-[14px] px-5 py-3 rounded-lg shadow-[0_0_0_1px_rgba(255,255,255,0.1)_inset,0_8px_24px_-8px_rgba(77,141,255,0.6)] hover:brightness-110 transition-all"
              >
                <Github className="w-4 h-4" />
                Install Agent Orchestrator
                <ArrowRight className="w-4 h-4 transition-transform group-hover:translate-x-0.5" />
              </a>
              <a
                href="https://github.com/AgentWrapper/agent-orchestrator/blob/main/docs/architecture.md"
                target="_blank"
                rel="noreferrer"
                data-testid="hero-secondary-cta"
                className="inline-flex items-center gap-2 bg-[color:var(--bg-card)] text-[color:var(--fg)] font-semibold text-[14px] px-5 py-3 rounded-lg border border-[color:var(--border-strong)] hover:bg-[color:var(--bg-card-hover)] transition-colors"
              >
                <BookOpen className="w-4 h-4" />
                Read the docs
              </a>
            </motion.div>

            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              transition={{ duration: 0.5, delay: 0.5 }}
              data-testid="hero-stats"
              className="mt-10 grid grid-cols-3 gap-3 max-w-md"
            >
              <Stat icon={Star} value="7.7k" label="stars" />
              <Stat icon={GitFork} value="1.1k" label="forks" />
              <Stat icon={Users} value="44+" label="contribs" />
            </motion.div>
          </div>

          {/* RIGHT - Live fleet */}
          <motion.div
            initial={{ opacity: 0, y: 16 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.7, delay: 0.25 }}
            className="lg:col-span-6 h-full flex items-center"
            data-testid="hero-fleet"
          >
            <div className="surface-elev p-4 glow-accent w-full min-h-[470px] lg:min-h-[560px] flex flex-col">
              {/* chrome */}
              <div className="flex items-center justify-between px-1 pb-2 font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--fg-dim)]">
                <span className="flex items-center gap-1.5">
                  <span className="w-1.5 h-1.5 rounded-full bg-[color:var(--status-ok)] pulse-dot" />
                  session.fleet · 4 active
                </span>
                <span>uptime 04:21:33</span>
              </div>
              {/* grid */}
              <div className="grid grid-cols-2 grid-rows-2 gap-2.5 flex-1">
                {scripts.map((s, i) => (
                  <AgentPane key={s.agent} script={s} index={i} />
                ))}
              </div>
              {/* footer */}
              <div className="mt-2.5 px-1 pt-2 border-t border-[color:var(--border)] flex items-center justify-between font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--fg-dim)]">
                <span className="flex items-center gap-1.5">
                  <span className="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] pulse-dot" />
                  cdc.stream
                </span>
                <span>127.0.0.1:3001</span>
              </div>
            </div>
          </motion.div>
        </div>
      </div>
    </section>
  );
}

function Stat({ icon: Icon, value, label }) {
  return (
    <div className="surface px-4 py-3 lift">
      <div className="flex items-center gap-1.5 text-[color:var(--fg-dim)] mb-1">
        <Icon className="w-3 h-3" />
        <span className="font-mono text-[9px] uppercase tracking-[0.22em]">{label}</span>
      </div>
      <div className="font-display font-bold text-2xl tracking-tight leading-none text-[color:var(--fg)]">
        {value}
      </div>
    </div>
  );
}
