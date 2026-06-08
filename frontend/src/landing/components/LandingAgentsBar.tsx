const agents = [
  {
    name: "Claude Code",
    src: "/docs/logos/claude-code.svg",
    alt: "Anthropic",
  },
  {
    name: "Codex",
    src: "/docs/logos/codex.svg",
    alt: "OpenAI",
  },
  {
    name: "Cursor",
    src: "/docs/logos/cursor.svg",
    alt: "Cursor",
  },
  {
    name: "Aider",
    src: "https://aider.chat/assets/logo.svg",
    alt: "Aider",
  },
  {
    name: "OpenCode",
    src: "/docs/logos/opencode.svg",
    alt: "OpenCode",
  },
];

export function LandingAgentsBar() {
  return (
    <div className="landing-reveal text-center px-6 pt-[96px]">
      <div className="text-xs tracking-[0.18em] uppercase text-[var(--landing-muted)] opacity-60 mb-9">
        Works with your favorite AI agents
      </div>
      <div className="flex items-center justify-center gap-6 sm:gap-9 flex-wrap max-w-[56rem] mx-auto">
        {agents.map((agent) => (
          <div key={agent.name} className="group flex flex-col items-center gap-3.5">
            <div className="flex items-center justify-center w-[5.5rem] h-[5.5rem] rounded-2xl border border-[var(--landing-border-subtle)] bg-[rgba(255,255,255,0.02)] transition-colors group-hover:border-[var(--landing-border-default)]">
              <img
                src={agent.src}
                alt={agent.alt}
                className="w-12 h-12 rounded-md object-contain"
              />
            </div>
            <div className="text-sm font-mono text-[var(--landing-muted)]">{agent.name}</div>
          </div>
        ))}
      </div>
    </div>
  );
}
