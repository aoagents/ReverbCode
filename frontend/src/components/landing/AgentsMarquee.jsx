import React from "react";

const row1 = [
  "Claude Code", "Codex", "Cursor", "OpenCode", "Aider", "Amp",
  "Goose", "Copilot", "Grok", "Qwen", "Kimi",
];
const row2 = [
  "Crush", "Cline", "Droid", "Devin", "Auggie", "Continue", "Kiro", "Kilocode",
  "Roo", "Plandex", "Sweep", "Tabby",
];

export default function AgentsMarquee() {
  const r1 = [...row1, ...row1];
  const r2 = [...row2, ...row2];
  return (
    <section
      id="agents"
      data-testid="agents-marquee"
      className="relative border-y border-[color:var(--border)] bg-[color:var(--bg-deep)] overflow-hidden"
    >
      <div className="container-page py-12 flex flex-wrap items-baseline gap-5 justify-between">
        <div className="flex items-baseline gap-4">
          <span className="serial-num text-xs font-mono">01 - coverage</span>
          <h2 className="font-display font-bold text-2xl sm:text-3xl tracking-tight leading-none text-[color:var(--fg)]">
            One daemon. <span className="text-[color:var(--fg-muted)]">Twenty-three agent harnesses.</span>
          </h2>
        </div>
        <p className="font-mono text-xs text-[color:var(--fg-dim)] max-w-md leading-relaxed">
          Swap harnesses per project. The daemon doesn&apos;t care which CLI is in the pane -
          adapters obey one port.
        </p>
      </div>

      <div className="relative space-y-3 pb-12">
        <div className="marquee-track">
          {r1.map((a, i) => (
            <AgentChip key={`a-${i}`} name={a} />
          ))}
        </div>
        <div className="marquee-track marquee-track-reverse marquee-track-slow">
          {r2.map((a, i) => (
            <AgentChip key={`b-${i}`} name={a} />
          ))}
        </div>
      </div>

      <div className="container-page pb-8 flex flex-wrap items-center justify-between gap-3 font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--fg-dim)]">
        <span>↳ registry · backend/internal/adapters/agent/</span>
        <span className="hidden sm:inline">↳ swap with <span className="text-[color:var(--accent)]">--worker-agent &lt;id&gt;</span></span>
        <span>↳ 23 shipped</span>
      </div>
    </section>
  );
}

function AgentChip({ name }) {
  return (
    <div className="mx-1.5 inline-flex items-center gap-2 px-4 py-2 rounded-full border border-[color:var(--border-strong)] bg-[color:var(--bg-card)] hover:border-[color:var(--accent)] hover:bg-[color:var(--accent-soft)] transition-colors whitespace-nowrap">
      <span className="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)]" />
      <span className="font-mono text-[12px] font-medium text-[color:var(--fg)]">{name}</span>
      <span className="font-mono text-[10px] text-[color:var(--fg-dim)]">/adapter</span>
    </div>
  );
}
