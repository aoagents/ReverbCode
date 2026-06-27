import React from "react";

const agents = [
  {
    name: "Claude Code",
    src: "/docs/logos/claude-code.svg",
    alt: "Claude Code",
    className: "h-7 sm:h-8 lg:h-10",
  },
  {
    name: "Codex",
    src: "/docs/logos/codex.svg",
    alt: "Codex",
    className: "h-7 sm:h-8 lg:h-10 rounded-md",
  },
  {
    name: "Cursor",
    src: "/docs/logos/cursor.svg",
    alt: "Cursor",
    className: "h-7 sm:h-8 lg:h-10",
  },
  {
    name: "Aider",
    src: "/docs/logos/aider.png",
    alt: "Aider",
    className: "h-6 sm:h-7 lg:h-8",
  },
  {
    name: "OpenCode",
    src: "/docs/logos/opencode.svg",
    alt: "OpenCode",
    className: "h-7 sm:h-8 lg:h-10",
  },
];

export default function AgentsMarquee() {
  return (
    <section
      id="agents"
      data-testid="agents-marquee"
      className="relative border-y border-[color:var(--border)] bg-[color:var(--bg-deep)] overflow-hidden"
    >
      <div className="container-page py-7 flex flex-wrap items-baseline gap-5 justify-between">
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

      <div className="container-page pb-6">
        <div className="mx-auto grid max-w-2xl grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-x-0 gap-y-4 items-end justify-items-center">
          {agents.map((agent) => (
            <AgentLogo key={agent.name} agent={agent} />
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

function AgentLogo({ agent }) {
  return (
    <div className="group flex min-h-[60px] w-full flex-col items-center justify-end gap-2">
      <div className="flex h-9 sm:h-10 lg:h-11 items-end justify-center">
        <img
          src={agent.src}
          alt={agent.alt}
          className={`${agent.className} max-w-[56px] object-contain transition-transform duration-300 group-hover:-translate-y-0.5`}
        />
      </div>
      <div className="font-mono text-[14px] sm:text-[16px] lg:text-[18px] leading-none tracking-[0.06em] text-[color:var(--fg-dim)]">
        {agent.name}
      </div>
    </div>
  );
}
