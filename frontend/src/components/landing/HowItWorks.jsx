import React from "react";
import { motion } from "framer-motion";

const steps = [
  {
    n: "01",
    label: "register",
    title: "Tell ao about your repo",
    desc: "Point the daemon at a local git repo. Worker and orchestrator agents are picked per project - no global setting wars.",
    code: `ao project add --path . \\
  --worker-agent codex \\
  --orchestrator-agent claude-code`,
    out: `✓ project "your-repo" registered
✓ data dir → ~/.ao/`,
  },
  {
    n: "02",
    label: "spawn",
    title: "Carve out a worktree, attach a pane",
    desc: "Every spawn creates its own git worktree and a tmux/zellij pane. Multiple sessions, zero collisions.",
    code: `ao spawn --prompt \\
  "Add SSO via Okta to /auth"`,
    out: `✓ session sess_8f2 spawned
✓ worktree wt-add-sso-okta
✓ pane attached · streaming activity`,
  },
  {
    n: "03",
    label: "ship",
    title: "Agent pushes the PR. You go for coffee.",
    desc: "The agent develops, tests, and opens a PR from inside its worktree. Activity streams back to your terminal or the desktop app.",
    code: `# inside the worktree, the agent runs:
git push -u origin add-sso-okta
gh pr create --fill`,
    out: `PR #482 opened
checks: queued · 0/4 complete`,
  },
  {
    n: "04",
    label: "react",
    title: "Feedback routes itself",
    desc: "The SCM observer watches the PR. CI failure, requested change, merge conflict - all become nudges to the owning agent. You only step in when the loop can't.",
    code: `[scm/github]  PR #482 · check "lint" → fail
[lcm]         derive nudge for sess_8f2
[agent/codex] received nudge · fix in progress`,
    out: `✓ lint passing · pushed fixup
✓ pr.merged → main`,
    accent: true,
  },
];

export default function HowItWorks() {
  return (
    <section
      id="how"
      data-testid="how-it-works"
      className="relative py-24 sm:py-32 border-t border-[color:var(--border)] bg-[color:var(--bg-deep)]"
    >
      <div className="container-page">
        <div className="grid lg:grid-cols-12 gap-8 mb-14 items-end">
          <div className="lg:col-span-7">
            <div className="serial-num text-xs font-mono mb-3">03 - how it works</div>
            <h2 className="font-display font-bold tracking-tight leading-[1.02] text-[color:var(--fg)]" style={{ fontSize: "clamp(32px, 4.8vw, 60px)" }}>
              Four commands.{" "}
              <span className="font-editorial italic font-medium text-[color:var(--accent)]">A fleet at work.</span>
            </h2>
          </div>
          <div className="lg:col-span-5">
            <p className="text-[15px] leading-relaxed text-[color:var(--fg-muted)]">
              No control plane. No SaaS account. No Docker network to debug. One Go binary,
              your favorite agent CLI, and the orchestrator runs on loopback.
            </p>
          </div>
        </div>

        <div className="relative space-y-5 lg:space-y-0">
          {steps.map((s, i) => (
            <Step key={s.n} s={s} i={i} />
          ))}
        </div>
      </div>
    </section>
  );
}

function Step({ s, i }) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 34, rotateX: -4 }}
      whileInView={{ opacity: 1, y: 0, rotateX: 0 }}
      whileHover={{ y: -6, scale: 1.01 }}
      viewport={{ once: false, amount: 0.42 }}
      transition={{ duration: 0.55, delay: i * 0.04, ease: [0.22, 1, 0.36, 1] }}
      data-testid={`step-${s.n}`}
      style={{
        top: `calc(88px + ${i * 18}px)`,
        zIndex: 10 + i,
      }}
      className={`grid lg:grid-cols-12 surface overflow-hidden transform-gpu lg:sticky lg:mb-6 ${
        s.accent ? "glow-accent" : ""
      }`}
    >
      <div className="lg:col-span-5 p-6 sm:p-8 border-b lg:border-b-0 lg:border-r border-[color:var(--border)]">
        <div className="flex items-center gap-3 mb-4">
          <span className="font-mono text-[11px] uppercase tracking-[0.25em] text-[color:var(--fg-dim)]">
            step {s.n}
          </span>
          <span className="h-px flex-1 bg-[color:var(--border)]" />
          <span
            className={`inline-block px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.22em] rounded ${
              s.accent
                ? "bg-[color:var(--accent-soft)] text-[color:var(--accent)]"
                : "bg-[color:var(--bg-deep)] text-[color:var(--fg-muted)] border border-[color:var(--border-strong)]"
            }`}
          >
            {s.label}
          </span>
        </div>
        <h3 className="font-display font-bold text-[22px] sm:text-[26px] tracking-tight leading-tight text-[color:var(--fg)] mb-3">
          {s.title}
        </h3>
        <p className="text-[14.5px] leading-relaxed text-[color:var(--fg-muted)]">
          {s.desc}
        </p>
      </div>

      <div className="lg:col-span-7 bg-[color:var(--code-bg)] p-6 sm:p-8 font-mono text-[12.5px] leading-relaxed border-l border-[color:var(--border)]">
        <div className="flex items-center gap-2 mb-3 text-[10px] uppercase tracking-[0.22em] text-[color:var(--code-muted)]">
          <span className="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)]" />
          <span>~/projects/your-repo</span>
          <span className="ml-auto">{s.label}.sh</span>
        </div>
        <pre className="whitespace-pre-wrap break-words text-[color:var(--code-fg)]">
{s.code}
        </pre>
        <div className="mt-3 pt-3 border-t border-[color:var(--border)] text-[color:var(--code-muted)]">
          <pre className="whitespace-pre-wrap break-words">
{s.out}
          </pre>
        </div>
      </div>
    </motion.div>
  );
}
