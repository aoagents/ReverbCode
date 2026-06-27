# ReverbCode Landing Page — PRD

## Original Problem Statement
"build best landing page for this open source product https://github.com/AgentWrapper/agent-orchestrator"

## User Choices
- Visual style: Light, modern SaaS-style
- Sections: hero, features, CTA + testimonials/social proof, architecture diagram, code examples / live demo
- Backend: Static only (no backend)

## Product Summary
Public name: **ReverbCode** (repo: AgentWrapper/agent-orchestrator). Orchestration layer for parallel AI coding agents. Go daemon + Electron supervisor + `ao` CLI. 7.7k stars, 1.1k forks. 23+ agent adapters (claude-code, codex, cursor, etc.), git-worktree isolation, live PR observation, loopback-only daemon.

## Architecture
- Single-page React app at `/`
- No backend changes; uses default FastAPI server
- Fonts: Cabinet Grotesk (display), IBM Plex Sans (body), JetBrains Mono (code)
- Palette: stark white #FAFAFA · deep slate #0F172A · warm coral accent #E07A5F (NO purple/teal)

## Implemented Sections (2026-12-26)
- `Nav` (sticky, mobile toggle)
- `Hero` (asymmetric, terminal mockup, GitHub stats)
- `AgentsMarquee` (two-row infinite scroll, 19 agents)
- `Features` (bento grid, 7 cards, dark accent card)
- `HowItWorks` (4 numbered steps with code blocks)
- `Architecture` (Swiss diagram: clients → daemon → 6 ports)
- `LiveDemo` (3 tabbed code examples + copy button)
- `SocialProof` (4 PR-style testimonial cards + stats)
- `CTA` (dark slab with coral offset shadow)
- `Footer` (4-column with product links)

## Testing
- iteration_1.json: 100% frontend pass, no functional issues.

## Backlog / P1
- Optional: scroll-spy + aria-current on active nav link
- Optional: catch-all 404 route
- Optional: OG meta + favicon refresh
- Optional: GitHub stars/contributors live fetch (would require small backend or use unauthenticated GH API client-side)

## P2
- Newsletter/waitlist (would need MongoDB backend — currently scoped out)
- Live ao demo / WASM playground
