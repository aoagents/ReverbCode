"use client";

import { useEffect, useRef, useState } from "react";

// The "before": scattered, faintly drifting session cards — the pain in the
// headline made visual. Same dark-card vocabulary as the rest of the site, so
// it reads as restless work, not clip-art browser tabs.
type Tone = "fail" | "warn" | "idle";

const chaos: {
  tab: string;
  line: string;
  tone: Tone;
  rot: string;
  x: string;
  y: string;
  z: number;
  dur: string;
  delay: string;
}[] = [
  { tab: "tab · #44 ci", line: "tests/auth ✗", tone: "fail", rot: "-4deg", x: "2%", y: "4%", z: 5, dur: "5.6s", delay: "0s" },
  { tab: "tab · #42 pr", line: "review pending", tone: "warn", rot: "3deg", x: "44%", y: "0%", z: 6, dur: "6.4s", delay: "0.7s" },
  { tab: "tab · #46 merge", line: "merge conflict", tone: "fail", rot: "-2deg", x: "14%", y: "35%", z: 7, dur: "5.9s", delay: "1.2s" },
  { tab: "tab · #43 api", line: "rate-limited", tone: "idle", rot: "5deg", x: "56%", y: "41%", z: 4, dur: "6.7s", delay: "0.3s" },
  { tab: "tab · logs", line: "copy-pasting…", tone: "idle", rot: "-3deg", x: "4%", y: "66%", z: 6, dur: "5.3s", delay: "0.9s" },
  { tab: "tab · #51 ci", line: "queued", tone: "warn", rot: "2deg", x: "46%", y: "68%", z: 5, dur: "6.1s", delay: "1.5s" },
];

const toneDot: Record<Tone, string> = {
  fail: "rgba(248,113,113,0.85)",
  warn: "rgba(251,191,36,0.8)",
  idle: "rgba(168,162,158,0.5)",
};

const config: [string, string][] = [
  ["agent", "claude-code"],
  ["tracker", "github"],
  ["workspace", "worktree"],
  ["runtime", "tmux"],
  ["notifier", "slack"],
];

export function LandingAbout() {
  // Reveal the calm config line-by-line once the section scrolls into view —
  // a deliberate assembling, never a typewriter.
  const ref = useRef<HTMLDivElement | null>(null);
  const [shown, setShown] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const obs = new IntersectionObserver(
      ([e]) => {
        if (e.isIntersecting) {
          setShown(true);
          obs.disconnect();
        }
      },
      { threshold: 0.4 },
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, []);

  return (
    <div className="bg-[radial-gradient(ellipse_at_top,rgba(255,240,220,0.015)_0%,transparent_70%)]">
      <section className="landing-reveal pt-[100px] pb-0 px-6 max-w-[72rem] mx-auto">
        <div className="text-xs tracking-[0.15em] uppercase text-[var(--landing-muted-dim)] mb-6 font-mono">
          The problem
        </div>
        <h2 className="font-sans font-[680] text-[clamp(1.375rem,3vw,2rem)] leading-[1.1] tracking-[-1.5px] mb-14 max-w-[48rem]">
          You&apos;re running AI agents in 10 browser tabs.{" "}
          <span className="text-[var(--landing-muted)]">
            Checking if PRs landed. Re-running failed CI. Copy-pasting error logs.
          </span>
        </h2>

        <div ref={ref} className="grid grid-cols-1 md:grid-cols-2 gap-12 items-start">
          {/* before — the drifting chaos pile */}
          <div className="relative h-[360px] md:h-[400px]" aria-hidden="true">
            {chaos.map((c) => (
              <div
                key={c.tab}
                className="landing-tab absolute w-[12.5rem]"
                style={
                  {
                    left: c.x,
                    top: c.y,
                    zIndex: c.z,
                    "--rot": c.rot,
                    "--dur": c.dur,
                    "--delay": c.delay,
                  } as React.CSSProperties
                }
              >
                <div className="rounded-xl border border-[var(--landing-border-subtle)] bg-[#1a1917] overflow-hidden shadow-[0_10px_30px_-12px_rgba(0,0,0,0.6)]">
                  <div className="flex items-center gap-2 px-3 h-7 border-b border-[var(--landing-border-subtle)]">
                    <span
                      className={`w-1.5 h-1.5 rounded-full shrink-0 ${c.tone === "fail" ? "landing-sse-pulse" : ""}`}
                      style={{ background: toneDot[c.tone] }}
                    />
                    <span className="font-mono text-[0.625rem] text-[var(--landing-muted-dim)] truncate">
                      {c.tab}
                    </span>
                  </div>
                  <div className="px-3 py-2.5">
                    <span
                      className="font-mono text-[0.6875rem]"
                      style={{
                        color:
                          c.tone === "fail"
                            ? "rgba(248,113,113,0.8)"
                            : "var(--landing-muted)",
                      }}
                    >
                      {c.line}
                    </span>
                  </div>
                </div>
              </div>
            ))}
          </div>

          {/* after — one still YAML file + the resolution copy */}
          <div>
            <div className="landing-card rounded-2xl overflow-hidden">
              <div className="flex items-center gap-2 px-4 py-2.5 border-b border-[var(--landing-border-subtle)]">
                <div className="w-2 h-2 rounded-full bg-[rgba(255,240,220,0.12)]" />
                <div className="w-2 h-2 rounded-full bg-[rgba(255,240,220,0.12)]" />
                <div className="w-2 h-2 rounded-full bg-[rgba(255,240,220,0.12)]" />
                <span className="ml-1.5 font-mono text-[0.5625rem] text-[var(--landing-muted-dim)]">
                  agent-orchestrator.yaml
                </span>
              </div>
              <div className="px-5 py-4 font-mono text-[0.75rem] leading-[1.9]">
                {config.map(([k, v], i) => (
                  <div
                    key={k}
                    style={{
                      opacity: shown ? 1 : 0,
                      transform: shown ? "translateY(0)" : "translateY(6px)",
                      transition: "opacity 0.5s ease, transform 0.5s ease",
                      transitionDelay: `${i * 90}ms`,
                    }}
                  >
                    <span className="text-[var(--landing-muted-dim)]">{k}:</span>{" "}
                    <span className="text-[var(--landing-fg)]">{v}</span>
                  </div>
                ))}
              </div>
            </div>

            <p className="mt-6 text-[0.9375rem] text-[var(--landing-muted)] leading-[1.8]">
              Agent Orchestrator replaces that with one YAML file. Point it at your
              GitHub issues, pick your agents, and walk away. Each agent spawns in
              its own git worktree, creates PRs, fixes CI failures, addresses review
              comments, and moves toward merge. If you are new, start with the{" "}
              <a
                href="/docs/"
                className="underline decoration-[var(--landing-border-default)] underline-offset-4 hover:text-white"
              >
                docs quickstart and configuration guides
              </a>
              .
            </p>
          </div>
        </div>
      </section>
    </div>
  );
}
