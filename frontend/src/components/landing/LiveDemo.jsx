import React, { useEffect, useState } from "react";
import { Copy, Check, Terminal, Box, Zap } from "lucide-react";

const tabs = [
  {
    id: "install",
    label: "Install",
    icon: Terminal,
    lines: [
      "# Requires Go 1.25+, tmux on PATH",
      "$ cd backend && go build -o /tmp/ao ./cmd/ao",
      "",
      "# Start the daemon and wait for /readyz",
      "$ /tmp/ao start",
      "✓ daemon up on 127.0.0.1:3001",
      "✓ pid 4821 · ready in 184ms",
      "",
      "$ /tmp/ao doctor",
      "✓ git           found · 2.43.0",
      "✓ tmux          found · 3.5a",
      "✓ data dir      ~/.ao",
      "✓ all checks    passing",
    ],
  },
  {
    id: "spawn",
    label: "Spawn agents",
    icon: Box,
    lines: [
      "# Register a local repo with worker + orchestrator",
      "$ ao project add --path /path/to/repo --id myrepo \\",
      "      --worker-agent codex \\",
      "      --orchestrator-agent claude-code",
      "✓ project \"myrepo\" registered",
      "",
      "# Fan a task out across parallel sessions",
      "$ ao spawn --project myrepo \\",
      "      --prompt \"Refactor auth to use JWT\"",
      "✓ session sess_8f2 · worktree wt-jwt",
      "",
      "$ ao session ls --project myrepo",
      "  sess_8f2  codex        wt-jwt        active",
      "  sess_a13  claude-code  wt-add-sso    active",
      "  sess_c01  cursor       wt-webhooks   active",
    ],
  },
  {
    id: "ci",
    label: "Auto-nudge on CI",
    icon: Zap,
    lines: [
      "# the SCM observer is already running. no flags needed.",
      "# when GitHub Actions fails:",
      "",
      "[scm/github]  PR #482 · check \"lint\" → fail",
      "[lcm]         derive nudge for sess_8f2",
      "[agent/codex] received nudge · resuming pane",
      "",
      "# the agent re-opens the worktree, fixes the lint,",
      "# pushes a new commit, and CI re-runs.",
      "# you do nothing.",
      "",
      "[scm/github]  PR #482 · check \"lint\" → pass",
      "[scm/github]  PR #482 · merged → main",
      "✓ ship it.",
    ],
  },
];

function classify(line) {
  if (!line) return "blank";
  if (line.startsWith("✓")) return "ok";
  if (line.startsWith("✗") || line.startsWith("⚠")) return "warn";
  if (line.startsWith("$")) return "cmd";
  if (line.startsWith("#")) return "comment";
  if (line.startsWith("[")) return "log";
  return "out";
}

function Typewriter({ lines }) {
  const [shown, setShown] = useState([]);
  const [lineIdx, setLineIdx] = useState(0);
  const [charIdx, setCharIdx] = useState(0);

  useEffect(() => {
    setShown([]);
    setLineIdx(0);
    setCharIdx(0);
  }, [lines]);

  useEffect(() => {
    if (lineIdx >= lines.length) return;
    const cur = lines[lineIdx] || "";
    if (charIdx <= cur.length) {
      const delay = cur.startsWith("$") || cur.startsWith("[") ? 14 : 10;
      const t = setTimeout(() => setCharIdx((c) => c + 1), delay + (cur.length === 0 ? 80 : 0));
      return () => clearTimeout(t);
    }
    const t = setTimeout(() => {
      setShown((s) => [...s, cur]);
      setLineIdx((l) => l + 1);
      setCharIdx(0);
    }, 80);
    return () => clearTimeout(t);
  }, [charIdx, lineIdx, lines]);

  const current = lines[lineIdx] || "";
  const partial = current.slice(0, charIdx);

  const colorFor = (k) =>
    k === "ok"
      ? "text-[color:var(--status-ok)]"
      : k === "warn"
      ? "text-[color:var(--status-fail)]"
      : k === "cmd"
      ? "text-[color:var(--code-fg)]"
      : k === "comment"
      ? "text-[color:var(--code-muted)]"
      : k === "log"
      ? "text-[color:var(--code-muted)]"
      : "text-[color:var(--code-muted)]";

  return (
    <div className="font-mono text-[13px] leading-[1.8] min-h-[380px]">
      {shown.map((l, i) => (
        <div key={i} className={`${colorFor(classify(l))} whitespace-pre-wrap`}>
          {l || "\u00A0"}
        </div>
      ))}
      {lineIdx < lines.length && (
        <div className={`${colorFor(classify(current))} whitespace-pre-wrap`}>
          {partial || "\u00A0"}
          <span className="caret" />
        </div>
      )}
    </div>
  );
}

export default function LiveDemo() {
  const [active, setActive] = useState("install");
  const [copied, setCopied] = useState(false);
  const current = tabs.find((t) => t.id === active);

  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText(current.lines.join("\n"));
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch (e) {
      /* noop */
    }
  };

  return (
    <section
      id="quickstart"
      data-testid="live-demo-terminal"
      className="relative py-24 sm:py-32 border-t border-[color:var(--border)] bg-[color:var(--bg-deep)]"
    >
      <div className="container-page">
        <div className="grid lg:grid-cols-12 gap-8 mb-14 items-end">
          <div className="lg:col-span-7">
            <div className="serial-num text-xs font-mono mb-3">05 - quickstart</div>
            <h2 className="font-display font-bold tracking-tight leading-[1.02] text-[color:var(--fg)]" style={{ fontSize: "clamp(32px, 4.8vw, 60px)" }}>
              From zero to a fleet -{" "}
              <span className="font-editorial italic font-medium text-[color:var(--accent)]">in three commands.</span>
            </h2>
          </div>
          <div className="lg:col-span-5">
            <p className="text-[15px] leading-relaxed text-[color:var(--fg-muted)]">
              Live transcript. Click a tab - the daemon types it back.
            </p>
          </div>
        </div>

        <div className="terminal-window">
          <div className="terminal-header flex items-center justify-between px-3 py-2">
            <div className="flex items-center gap-1.5">
              <span className="w-3 h-3 rounded-full bg-[color:var(--dot-red)]" />
              <span className="w-3 h-3 rounded-full bg-[color:var(--dot-yellow)]" />
              <span className="w-3 h-3 rounded-full bg-[color:var(--dot-green)]" />
            </div>
            <span className="font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--code-muted)]">
              ao - {current.label.toLowerCase()}
            </span>
            <button
              onClick={onCopy}
              data-testid="demo-copy-btn"
              className="inline-flex items-center gap-1.5 px-2 py-1 rounded border border-[color:var(--border-strong)] font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--fg-muted)] hover:text-[color:var(--fg)] hover:border-[color:var(--border-bright)] transition"
            >
              {copied ? <Check className="w-3 h-3" /> : <Copy className="w-3 h-3" />}
              {copied ? "Copied" : "Copy"}
            </button>
          </div>

          {/* tab strip */}
          <div className="flex items-center gap-1 px-3 pt-3 bg-[color:var(--code-chrome)] border-b border-[color:var(--border)]">
            {tabs.map((t) => {
              const Icon = t.icon;
              const isActive = t.id === active;
              return (
                <button
                  key={t.id}
                  onClick={() => setActive(t.id)}
                  data-testid={`demo-tab-${t.id}`}
                  className={`inline-flex items-center gap-1.5 px-3 py-1.5 -mb-px rounded-t-md font-mono text-[11px] uppercase tracking-[0.18em] transition-colors border-x border-t ${
                    isActive
                      ? "bg-[color:var(--code-bg)] border-[color:var(--border-strong)] text-[color:var(--code-fg)]"
                      : "border-transparent text-[color:var(--code-muted)] hover:text-[color:var(--code-fg)]"
                  }`}
                >
                  <Icon className="w-3 h-3" />
                  {t.label}
                </button>
              );
            })}
          </div>

          <div data-testid="demo-code-block" className="p-5 sm:p-7 bg-[color:var(--code-bg)]">
            <Typewriter lines={current.lines} />
          </div>
        </div>
      </div>
    </section>
  );
}
