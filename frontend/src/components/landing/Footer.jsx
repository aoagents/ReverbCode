import React from "react";
import { Github } from "lucide-react";

const LOGO_URL = "https://customer-assets.emergentagent.com/job_orchestrator-hub-21/artifacts/buzj94q2_ao-logo.svg";

export default function Footer() {
  return (
    <footer
      data-testid="footer"
      className="bg-[color:var(--bg-deep)] border-t border-[color:var(--border)]"
    >
      <div className="container-page py-16">
        <div className="grid md:grid-cols-12 gap-10">
          <div className="md:col-span-5">
            <div className="flex items-center gap-2.5 mb-4">
              <img
                src={LOGO_URL}
                alt="Agent Orchestrator"
                className="w-10 h-10 object-contain"
              />
              <span className="font-display font-bold text-lg tracking-tight text-[color:var(--fg)]">
                Agent Orchestrator
              </span>
            </div>
            <p className="text-[14px] leading-relaxed text-[color:var(--fg-muted)] max-w-sm">
              The open-source orchestration layer for parallel AI coding agents. Loopback-only,
              Apache 2.0 licensed, runs on your laptop.
            </p>
          </div>

          <FooterCol
            title="Product"
            links={[
              { label: "Features", href: "#features" },
              { label: "How it works", href: "#how" },
              { label: "Architecture", href: "#architecture" },
              { label: "Quickstart", href: "#quickstart" },
            ]}
          />
          <FooterCol
            title="Resources"
            links={[
              { label: "GitHub", href: "https://github.com/AgentWrapper/agent-orchestrator" },
              { label: "Architecture docs", href: "/docs/architecture" },
              { label: "CLI reference", href: "/docs/cli" },
              { label: "Releases", href: "https://github.com/AgentWrapper/agent-orchestrator/releases" },
            ]}
          />
          <FooterCol
            title="Community"
            links={[
              { label: "Contributors", href: "https://github.com/AgentWrapper/agent-orchestrator/graphs/contributors" },
              { label: "Issues", href: "https://github.com/AgentWrapper/agent-orchestrator/issues" },
              { label: "Pull requests", href: "https://github.com/AgentWrapper/agent-orchestrator/pulls" },
              { label: "ao-agents.com", href: "https://ao-agents.com" },
            ]}
          />
        </div>

        <div className="mt-14 pt-6 border-t border-[color:var(--border)] flex flex-col sm:flex-row gap-3 items-start sm:items-center justify-between font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--fg-dim)]">
          <div>
            Built by the open-source community.
          </div>
          <a
            href="https://github.com/AgentWrapper/agent-orchestrator"
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 hover:text-[color:var(--accent)] transition-colors"
            data-testid="footer-github-link"
          >
            <Github className="w-3 h-3" />
            AgentWrapper/agent-orchestrator
          </a>
        </div>
      </div>
    </footer>
  );
}

function FooterCol({ title, links }) {
  return (
    <div className="md:col-span-2 lg:col-span-2">
      <h4 className="font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--fg-dim)] mb-4">
        {title}
      </h4>
      <ul className="space-y-2.5">
        {links.map((l) => (
          <li key={l.label}>
            <a
              href={l.href}
              target={l.href.startsWith("#") || l.href.startsWith("/") ? undefined : "_blank"}
              rel={l.href.startsWith("#") || l.href.startsWith("/") ? undefined : "noreferrer"}
              className="text-[13.5px] text-[color:var(--fg-muted)] hover:text-[color:var(--fg)] transition-colors"
            >
              {l.label}
            </a>
          </li>
        ))}
      </ul>
    </div>
  );
}
