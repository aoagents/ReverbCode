# agent-orchestrator (rewrite) — docs

The agent-orchestrator is being rebuilt as a long-running **Go backend daemon**
(`backend/`) plus an **Electron + TypeScript frontend** (`frontend/`). The
backend supervises a fleet of coding-agent sessions and keeps one true status
per session.

This folder documents the **Lifecycle Manager (LCM) + Session Manager (SM)
lane** — the deterministic core of the backend that is now implemented (behind
fakes) on the `feat/lcm-sm-contracts` integration branch.

## Start here

| Doc | What it covers |
|-----|----------------|
| [architecture.md](architecture.md) | How the lane works: the OBSERVE→DECIDE→ACT loop, the canonical state model, the package layout, every component, and the load-bearing invariants. Read this first. |
| [status.md](status.md) | What's done (PR by PR), what's left, the integration to-dos, the open cross-lane contract questions, and how to build/test. |
| [stack.md](stack.md) | Accepted library/runtime choices, pending stack decisions, and dependencies explicitly avoided for V1. |

## The one-paragraph mental model

The backend is a **stateless supervisor over external ground truth**: git/GitHub
own PR/CI/review truth, the agent's own files own its activity, and the backend
owns no agent state. Its whole job is, per session: **OBSERVE** raw facts →
**DECIDE** one canonical status via pure, deterministic functions → **ACT**
(persist + fire reactions). The LCM is that reducer; the SM is the
explicit-mutation plumbing (spawn/kill/restore/cleanup) that feeds it.

## Where this lane fits

Other lanes (built by other people, in parallel) provide the real adapters this
lane depends on through narrow interfaces: the **persistence layer + CDC**, the
**SCM poller**, the **runtime/agent/workspace plugins**, the **backend API +
OpenAPI**, and the **frontend store**. See [status.md](status.md#integration)
for the hand-off points.
