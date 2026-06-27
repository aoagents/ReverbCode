import React from "react";
import { ArrowRight, Github, BookOpen } from "lucide-react";
import { motion } from "framer-motion";
import { docsUrl } from "@/lib/docs-url";

export default function Hero() {
  return (
    <section
      data-testid="hero-section"
      id="top"
      className="relative overflow-hidden border-b border-[color:var(--border)] pt-14 pb-16 sm:pt-16"
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
      <div
        className="pointer-events-none absolute left-1/2 top-0 h-[520px] w-[960px] -translate-x-1/2 opacity-80"
        style={{
          background:
            "radial-gradient(ellipse at center, rgba(77,141,255,0.22), transparent 62%)",
        }}
      />

      <div className="container-page relative z-10">
        <div className="w-full">
          <div className="mx-auto max-w-6xl text-center">
            <motion.h1
              initial={{ opacity: 0, y: 16 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6 }}
              data-testid="hero-headline"
              className="font-display font-bold leading-[0.95] tracking-tight text-[color:var(--fg)]"
              style={{ fontSize: "clamp(48px, 6.4vw, 92px)" }}
            >
              Worktrees for the work.
              <span className="block text-[color:var(--fg-muted)]">Review loops for the finish.</span>
            </motion.h1>

            <motion.p
              initial={{ opacity: 0, y: 12 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6, delay: 0.15 }}
              data-testid="hero-subtitle"
              className="mx-auto mt-5 max-w-4xl text-[17px] font-semibold leading-[1.55] text-[color:var(--fg-muted)] sm:text-[19px]"
            >
              AO gives every agent a clean checkout, follows the PR lifecycle, and routes red CI
              or requested changes back into the session that owns them.
            </motion.p>

            <motion.div
              initial={{ opacity: 0, y: 12 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.6, delay: 0.32 }}
              className="mt-7 flex flex-wrap items-center justify-center gap-3"
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
                href={docsUrl()}
                data-testid="hero-secondary-cta"
                className="inline-flex items-center gap-2 bg-[color:var(--bg-card)] text-[color:var(--fg)] font-semibold text-[14px] px-5 py-3 rounded-lg border border-[color:var(--border-strong)] hover:bg-[color:var(--bg-card-hover)] transition-colors"
              >
                <BookOpen className="w-4 h-4" />
                Read the docs
              </a>
            </motion.div>
          </div>

          <motion.div
            initial={{ opacity: 0, y: 18 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.7, delay: 0.42 }}
            className="mx-auto mt-10 max-w-7xl"
            data-testid="hero-screenshot"
          >
            <div className="overflow-hidden rounded-2xl border border-[color:var(--border-strong)] bg-[color:var(--bg-deep)]">
              <img
                src="/hero-dashboard.png"
                alt="Agent Orchestrator dashboard board view"
                className="block w-full"
                draggable="false"
              />
            </div>
          </motion.div>
        </div>
      </div>
    </section>
  );
}
