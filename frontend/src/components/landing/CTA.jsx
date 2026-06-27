import React from "react";
import { Github, ArrowRight, BookOpen } from "lucide-react";
import { docsUrl } from "@/lib/docs-url";

export default function CTA() {
  return (
    <section
      id="cta"
      data-testid="cta-section"
      className="relative py-24 sm:py-32 border-t border-[color:var(--border)] overflow-hidden"
    >
      <div
        className="pointer-events-none absolute inset-0 opacity-60"
        style={{
          background:
            "radial-gradient(ellipse at 50% 0%, rgba(77,141,255,0.18), transparent 60%)",
        }}
      />
      <div className="container-page relative">
        <div className="surface-elev px-8 sm:px-14 py-14 sm:py-20 text-center glow-accent">
          <div className="inline-flex items-center gap-2 mb-8 px-3 py-1 rounded-full border border-[color:var(--border-strong)] bg-[color:var(--bg-deep)]">
            <span className="font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--accent)]">
              $ ao spawn --prompt &quot;ship it&quot;
            </span>
          </div>

          <h2
            data-testid="cta-headline"
            className="font-display font-bold tracking-tight leading-[1.02] text-[color:var(--fg)]"
            style={{ fontSize: "clamp(36px, 6vw, 76px)" }}
          >
            Stop babysitting one agent.
            <br />
            <span className="font-editorial italic font-medium text-[color:var(--accent)]">
              Start orchestrating.
            </span>
          </h2>

          <p className="mt-6 max-w-2xl mx-auto text-[16px] sm:text-[17px] leading-relaxed text-[color:var(--fg-muted)]">
            Free, Apache 2.0 licensed, runs on your laptop. The whole repo is on GitHub - read the
            source, fork it, and ship your first parallel agent in five minutes.
          </p>

          <div className="mt-10 flex flex-wrap items-center justify-center gap-3">
            <a
              href="https://github.com/AgentWrapper/agent-orchestrator"
              target="_blank"
              rel="noreferrer"
              data-testid="cta-github-btn"
              className="group inline-flex items-center gap-2 bg-[color:var(--accent)] text-white font-semibold text-[15px] px-6 py-3.5 rounded-lg shadow-[0_0_0_1px_rgba(255,255,255,0.1)_inset,0_8px_24px_-8px_rgba(77,141,255,0.6)] hover:brightness-110 transition-all"
            >
              <Github className="w-4 h-4" />
              Star on GitHub · 7.7k
              <ArrowRight className="w-4 h-4 transition-transform group-hover:translate-x-0.5" />
            </a>
            <a
              href={docsUrl("architecture")}
              data-testid="cta-docs-btn"
              className="inline-flex items-center gap-2 bg-[color:var(--bg-deep)] text-[color:var(--fg)] font-semibold text-[15px] px-6 py-3.5 rounded-lg border border-[color:var(--border-strong)] hover:bg-[color:var(--bg-card-hover)] transition-colors"
            >
              <BookOpen className="w-4 h-4" />
              Read the architecture
            </a>
          </div>
        </div>
      </div>
    </section>
  );
}
