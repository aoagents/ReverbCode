import React from "react";
import { Menu, X, ArrowUpRight, Github, Moon, Sun } from "lucide-react";
import { motion } from "framer-motion";

const LOGO_URL = "https://customer-assets.emergentagent.com/job_orchestrator-hub-21/artifacts/buzj94q2_ao-logo.svg";

const navItems = [
  { label: "Features", href: "#features" },
  { label: "How it works", href: "#how" },
  { label: "Architecture", href: "#architecture" },
  { label: "Docs", href: "/docs" },
  { label: "Quickstart", href: "#quickstart" },
];

export default function Nav() {
  const [open, setOpen] = React.useState(false);
  const [theme, setTheme] = React.useState(() => {
    if (typeof window === "undefined") return "dark";
    return (
      window.localStorage.getItem("ao-theme") ||
      (window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark")
    );
  });
  const isLight = theme === "light";

  React.useEffect(() => {
    document.documentElement.dataset.theme = theme;
    window.localStorage.setItem("ao-theme", theme);
  }, [theme]);

  return (
    <header
      data-testid="site-nav"
      className="sticky top-0 z-40 bg-[color:var(--nav-bg)] backdrop-blur-xl border-b border-[color:var(--border)]"
    >
      <div className="container-page h-16 flex items-center justify-between">
        <a href="#top" data-testid="nav-logo" className="flex items-center gap-2.5 group">
          <img
            src={LOGO_URL}
            alt="Agent Orchestrator"
            className="w-10 h-10 object-contain"
          />
          <span className="font-display font-bold text-[15px] tracking-tight text-[color:var(--fg)]">
            Agent Orchestrator
          </span>
        </a>

        <nav className="hidden md:flex items-center gap-7">
          {navItems.map((item) => (
            <a
              key={item.label}
              href={item.href}
              data-testid={`nav-link-${item.label.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`}
              className="text-[13px] font-medium text-[color:var(--fg-muted)] hover:text-[color:var(--fg)] transition-colors"
            >
              {item.label}
            </a>
          ))}
        </nav>

        <div className="flex items-center gap-2">
          <a
            href="https://github.com/AgentWrapper/agent-orchestrator"
            target="_blank"
            rel="noreferrer"
            data-testid="nav-star-btn"
            className="hidden sm:inline-flex items-center gap-1.5 text-[12px] font-medium px-2.5 py-1.5 rounded-md border border-[color:var(--border-strong)] text-[color:var(--fg-muted)] hover:text-[color:var(--fg)] hover:border-[color:var(--border-bright)] transition-colors"
          >
            <Github className="w-3.5 h-3.5" />
            <span className="font-mono">7.7k</span>
          </a>
          <button
            type="button"
            onClick={() => setTheme(isLight ? "dark" : "light")}
            data-testid="theme-toggle"
            className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-[color:var(--border-strong)] text-[color:var(--fg-muted)] hover:text-[color:var(--fg)] hover:border-[color:var(--border-bright)] transition-colors"
            aria-label={isLight ? "Switch to dark theme" : "Switch to light theme"}
            title={isLight ? "Dark theme" : "Light theme"}
          >
            {isLight ? <Moon className="h-4 w-4" /> : <Sun className="h-4 w-4" />}
          </button>
          <a
            href="https://github.com/AgentWrapper/agent-orchestrator"
            target="_blank"
            rel="noreferrer"
            data-testid="nav-cta-btn"
            className="inline-flex items-center gap-1.5 text-[13px] font-semibold px-3.5 py-1.5 rounded-md bg-[color:var(--accent)] text-white hover:brightness-110 transition-all"
          >
            Install
            <ArrowUpRight className="w-3.5 h-3.5" />
          </a>
          <button
            onClick={() => setOpen(!open)}
            className="md:hidden p-1.5 rounded-md border border-[color:var(--border-strong)] text-[color:var(--fg)]"
            data-testid="nav-mobile-toggle"
            aria-label="menu"
          >
            {open ? <X className="w-4 h-4" /> : <Menu className="w-4 h-4" />}
          </button>
        </div>
      </div>
      {open && (
        <motion.div
          initial={{ opacity: 0, y: -8 }}
          animate={{ opacity: 1, y: 0 }}
          className="md:hidden border-t border-[color:var(--border)] bg-[color:var(--bg-card)]"
        >
          <div className="px-5 py-4 flex flex-col gap-3.5">
            {navItems.map((item) => (
              <a
                key={item.label}
                href={item.href}
                onClick={() => setOpen(false)}
                className="text-sm font-medium text-[color:var(--fg-muted)]"
              >
                {item.label}
              </a>
            ))}
          </div>
        </motion.div>
      )}
    </header>
  );
}
