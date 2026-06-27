import React from "react";
import { ArrowRight, Github, BookOpen } from "lucide-react";
import { motion } from "framer-motion";
import { docsUrl } from "@/lib/docs-url";

export default function Hero() {
  return (
    <section
      data-testid="hero-section"
      id="top"
      className="relative overflow-hidden border-b border-[color:var(--border)] pt-14 pb-10 sm:pt-16"
    >
      <div
        className="pointer-events-none absolute inset-0 opacity-[0.24]"
        style={{
          backgroundImage:
            "linear-gradient(var(--border) 1px, transparent 1px), linear-gradient(90deg, var(--border) 1px, transparent 1px)",
          backgroundSize: "44px 44px",
          maskImage: "radial-gradient(ellipse at 52% 42%, black 0%, transparent 68%)",
          WebkitMaskImage: "radial-gradient(ellipse at 52% 42%, black 0%, transparent 68%)",
        }}
      />
      <div className="relative z-10 mx-auto w-full max-w-[1680px] px-5 sm:px-8 lg:px-12 xl:px-16">
        <div className="grid items-center gap-10 lg:grid-cols-[0.9fr_1.1fr] lg:gap-10 xl:gap-14">
          <div className="max-w-[760px] text-left">
            <motion.div
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.5 }}
              className="mb-5 font-mono text-[11px] font-semibold uppercase tracking-[0.22em] text-[color:var(--accent)]"
            >
              Agent work, from issue to merge
            </motion.div>
            <motion.h1
              initial={{ opacity: 0, y: 16 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6 }}
              data-testid="hero-headline"
              className="font-display font-bold leading-[0.94] tracking-tight text-[color:var(--fg)]"
              style={{ fontSize: "clamp(54px, 5.45vw, 104px)" }}
            >
              <span className="block lg:whitespace-nowrap">Review the work,</span>
              <span className="block text-[color:var(--accent)] lg:whitespace-nowrap">
                Not the agents.
              </span>
            </motion.h1>

            <motion.p
              initial={{ opacity: 0, y: 12 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6, delay: 0.15 }}
              data-testid="hero-subtitle"
              className="mt-6 max-w-[720px] text-[18px] font-semibold leading-[1.62] text-[color:var(--fg-muted)] sm:text-[20px]"
            >
              Every issue gets its own checkout, session, branch, PR, checks, and review thread.
              When something breaks, the right context goes back to the right agent.
            </motion.p>

            <motion.div
              initial={{ opacity: 0, y: 12 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6, delay: 0.32 }}
              className="mt-8 flex flex-wrap items-center gap-3"
            >
              <a
                href="https://github.com/AgentWrapper/agent-orchestrator"
                target="_blank"
                rel="noreferrer"
                data-testid="hero-primary-cta"
                className="group inline-flex items-center gap-2 bg-[color:var(--accent)] text-white font-semibold text-[14px] px-5 py-3 rounded-lg shadow-[0_0_0_1px_rgba(255,255,255,0.1)_inset] hover:brightness-110 transition-all"
              >
                <Github className="w-4 h-4" />
                Install Agent Orchestrator
                <ArrowRight className="w-4 h-4 transition-transform group-hover:translate-x-0.5" />
              </a>
              <a
                href={docsUrl()}
                data-testid="hero-secondary-cta"
                className="inline-flex items-center gap-2 bg-[color:var(--bg-card)] text-[color:var(--fg)] font-semibold text-[14px] px-5 py-3 rounded-lg border border-[color:var(--border-strong)] hover:bg-[color:var(--bg-card-hover)] transition-colors"
              >
                <BookOpen className="w-4 h-4" />
                Read the docs
              </a>
            </motion.div>

            <motion.div
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6, delay: 0.42 }}
              className="mt-8 border-l border-[color:var(--border-strong)] pl-4 font-mono text-[11px] uppercase tracking-[0.16em] text-[color:var(--fg-dim)]"
            >
              issue -> worktree -> session -> pull request -> review loop
            </motion.div>
          </div>

          <motion.div
            initial={{ opacity: 0, y: 18 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.7, delay: 0.42 }}
            className="relative min-w-0"
            data-testid="hero-screenshot"
          >
            <div className="relative ml-auto w-full max-w-[1080px]">
              <div className="relative rounded-[18px] border border-[color:var(--border)] bg-[color:var(--bg-elevated)] p-1">
                <div className="overflow-hidden rounded-[13px] bg-[color:var(--bg-deep)]">
                  <img
                    src="/hero-dashboard.png"
                    alt="Agent Orchestrator dashboard board view"
                    className="theme-dark-only block w-full"
                    draggable="false"
                  />
                  <img
                    src="/hero-dashboard-light.png"
                    alt="Agent Orchestrator dashboard board view in light theme"
                    className="theme-light-only hidden w-full"
                    draggable="false"
                  />
                </div>
              </div>

              <div className="absolute -right-3 bottom-0 hidden w-[26%] min-w-[170px] translate-y-[12%] sm:block lg:-right-10">
                <div className="rounded-[24px] border border-[color:var(--border)] bg-[color:var(--bg-elevated)] p-1 shadow-[0_24px_80px_-48px_rgba(0,0,0,0.95)]">
                  <div className="relative overflow-hidden rounded-[19px] bg-[color:var(--bg-deep)]">
                    <img
                      src="/hero-new-task.png"
                      alt="ReverbCode mobile workflow preview"
                      className="theme-dark-only block aspect-[9/16] h-auto w-full object-cover object-center"
                      draggable="false"
                    />
                    <img
                      src="/hero-dashboard-light.png"
                      alt="ReverbCode mobile workflow preview in light theme"
                      className="theme-light-only hidden aspect-[9/16] h-auto w-full object-cover object-center"
                      draggable="false"
                    />
                  </div>
                </div>
              </div>
            </div>
          </motion.div>
        </div>
      </div>
    </section>
  );
}
