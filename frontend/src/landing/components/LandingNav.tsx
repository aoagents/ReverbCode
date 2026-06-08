"use client";

import { useEffect, useRef, useState } from "react";

function XIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z" />
    </svg>
  );
}

function DiscordIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M20.317 4.37a19.791 19.791 0 0 0-4.885-1.515.074.074 0 0 0-.079.037c-.21.375-.444.864-.608 1.25a18.27 18.27 0 0 0-5.487 0 12.64 12.64 0 0 0-.617-1.25.077.077 0 0 0-.079-.037A19.736 19.736 0 0 0 3.677 4.37a.07.07 0 0 0-.032.027C.533 9.046-.32 13.58.099 18.057a.082.082 0 0 0 .031.057 19.9 19.9 0 0 0 5.993 3.03.078.078 0 0 0 .084-.028 14.09 14.09 0 0 0 1.226-1.994.076.076 0 0 0-.041-.106 13.107 13.107 0 0 1-1.872-.892.077.077 0 0 1-.008-.128 10.2 10.2 0 0 0 .372-.292.074.074 0 0 1 .077-.01c3.928 1.793 8.18 1.793 12.062 0a.074.074 0 0 1 .078.01c.12.098.246.198.373.292a.077.077 0 0 1-.006.127 12.299 12.299 0 0 1-1.873.892.077.077 0 0 0-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.839 19.839 0 0 0 6.002-3.03.077.077 0 0 0 .032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 0 0-.031-.03zM8.02 15.33c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.956-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.956 2.418-2.157 2.418zm7.975 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.946 2.418-2.157 2.418z" />
    </svg>
  );
}

function GithubIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z" />
    </svg>
  );
}

const navLinks = [
  { label: "Docs", href: "/docs" },
  { label: "Features", href: "#features" },
  { label: "How It Works", href: "#how" },
];

export function LandingNav() {
  // The logo + right cluster collapse away on scroll-down and return on
  // scroll-up; the centered link pill stays put the whole time.
  const [collapsed, setCollapsed] = useState(false);

  // Scroll-spy: which hash section is currently in view (null over the hero).
  // The cursor takes over via `hovered`; the sliding highlight follows
  // whichever is set, falling back to the active section on mouse-out.
  const [active, setActive] = useState<number | null>(null);
  const [hovered, setHovered] = useState<number | null>(null);
  const driver = hovered ?? active;

  const itemRefs = useRef<Array<HTMLLIElement | null>>([]);
  const [pill, setPill] = useState({ left: 0, width: 0, visible: false });

  useEffect(() => {
    let lastY = window.scrollY;
    let raf = 0;
    const evaluate = () => {
      raf = 0;
      const y = window.scrollY;
      if (y < 80) setCollapsed(false);
      else if (y > lastY + 4) setCollapsed(true);
      else if (y < lastY - 4) setCollapsed(false);
      lastY = y;

      // Active section = last hash link whose section top has crossed a probe
      // line ~35% down the viewport. Route links (e.g. /docs) are skipped.
      const probe = y + window.innerHeight * 0.35;
      let found: number | null = null;
      navLinks.forEach((link, i) => {
        if (!link.href.startsWith("#")) return;
        const el = document.getElementById(link.href.slice(1));
        if (el && el.offsetTop <= probe) found = i;
      });
      setActive(found);
    };
    const onScroll = () => {
      if (!raf) raf = requestAnimationFrame(evaluate);
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    evaluate();
    return () => {
      window.removeEventListener("scroll", onScroll);
      if (raf) cancelAnimationFrame(raf);
    };
  }, []);

  // Reposition the sliding highlight under the driver link. Recomputed on
  // driver change and on resize (link offsets shift with viewport width).
  useEffect(() => {
    const place = () => {
      if (driver == null) {
        setPill((p) => ({ ...p, visible: false }));
        return;
      }
      const el = itemRefs.current[driver];
      if (!el) return;
      setPill({ left: el.offsetLeft, width: el.offsetWidth, visible: true });
    };
    place();
    window.addEventListener("resize", place);
    return () => window.removeEventListener("resize", place);
  }, [driver]);

  // Fade + slide for the side clusters; pointer-events off when hidden so the
  // collapsed (invisible) logo/icons aren't clickable.
  const sideStyle = (dir: "left" | "right"): React.CSSProperties => ({
    opacity: collapsed ? 0 : 1,
    transform: collapsed
      ? `translateX(${dir === "left" ? "-12px" : "12px"})`
      : "translateX(0)",
    pointerEvents: collapsed ? "none" : "auto",
    transition: "opacity 0.35s ease, transform 0.45s cubic-bezier(0.22,1,0.36,1)",
  });

  return (
    <nav className="fixed top-0 left-0 right-0 z-50">
      <div className="relative flex items-center justify-between px-8 py-6 max-w-[80rem] mx-auto">
        <a
          href="/"
          className="inline-flex items-center gap-2 text-base font-semibold text-white no-underline font-sans font-[680] tracking-tight"
          style={sideStyle("left")}
        >
          <img src="/ao-logo.svg" alt="" aria-hidden="true" width={28} height={28} className="h-7 w-7" />
          Agent Orchestrator
        </a>

        {/* Centered link pill — always visible */}
        <ul
          className="hidden md:flex items-center gap-1 list-none absolute left-1/2 -translate-x-1/2 rounded-full px-2 py-1.5 bg-[var(--landing-card-bg)]/80 backdrop-blur-md border border-[var(--landing-border-subtle)]"
          onMouseLeave={() => setHovered(null)}
        >
          {/* Single sliding highlight — glides between links, fades out over the hero */}
          <span
            aria-hidden="true"
            className="absolute top-1/2 -translate-y-1/2 h-[calc(100%-0.5rem)] rounded-full bg-white/[0.07] border border-white/[0.06]"
            style={{
              left: pill.left,
              width: pill.width,
              opacity: pill.visible ? 1 : 0,
              transition:
                "left 0.4s cubic-bezier(0.22,1,0.36,1), width 0.4s cubic-bezier(0.22,1,0.36,1), opacity 0.25s ease",
            }}
          />
          {navLinks.map((link, i) => (
            <li
              key={link.label}
              ref={(el) => {
                itemRefs.current[i] = el;
              }}
              onMouseEnter={() => setHovered(i)}
            >
              <a
                href={link.href}
                className={`relative z-10 block text-sm no-underline px-4 py-1.5 rounded-full transition-colors ${
                  driver === i ? "text-white" : "text-[var(--landing-muted)] hover:text-white"
                }`}
              >
                {link.label}
              </a>
            </li>
          ))}
        </ul>

        <div className="flex items-center gap-2" style={sideStyle("right")}>
          <a
            href="https://x.com/aoagents"
            target="_blank"
            rel="noopener noreferrer"
            aria-label="X (Twitter)"
            className="inline-flex h-9 w-9 items-center justify-center rounded-md text-white/80 transition-colors hover:text-white"
          >
            <XIcon />
          </a>
          <a
            href="https://discord.gg/UZv7JjxbwG"
            target="_blank"
            rel="noopener noreferrer"
            aria-label="Discord"
            className="inline-flex h-9 w-9 items-center justify-center rounded-md text-white/80 transition-colors hover:text-white"
          >
            <DiscordIcon />
          </a>
          <a
            href="https://github.com/ComposioHQ/agent-orchestrator"
            target="_blank"
            rel="noopener noreferrer"
            aria-label="GitHub"
            className="inline-flex h-9 w-9 items-center justify-center rounded-md text-white/80 transition-colors hover:text-white"
          >
            <GithubIcon />
          </a>
        </div>
      </div>
    </nav>
  );
}
