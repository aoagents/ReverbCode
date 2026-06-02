// agent-orchestrator: managed opencode activity plugin (do not edit)
//
// opencode has no native command-hook config (unlike Claude Code's
// settings.local.json or Codex's hooks.json), so AO installs this plugin into
// .opencode/plugins/ to report session lifecycle into AO's store for activity
// detection. The file is owned and overwritten by AO on every hook install;
// user-authored plugins live in their own files and are never touched.
//
// It maps opencode's native lifecycle events onto AO's three normalized
// activity events (matching the claude-code and codex adapters):
//   session.created                       -> `ao hooks opencode session-start`
//   message.updated / message.part.updated -> `ao hooks opencode user-prompt-submit`
//   session.status (status.type == idle)   -> `ao hooks opencode stop`
//
// The opencode-native session id (and prompt/model where known) is piped to the
// hook command as JSON on stdin, run with cwd set to the worktree so AO can
// correlate the opencode session to its AO session. Every invocation is
// best-effort: a missing `ao` binary or a failing hook must never crash the
// user's opencode session, so each call is guarded (`command -v ao`) and
// wrapped in try/catch.
//
// `import type` is erased at runtime by Bun's transpiler, so this loads even
// before opencode has installed @opencode-ai/plugin into the config dir.
import type { Plugin } from "@opencode-ai/plugin"

export const aoActivity: Plugin = async ({ directory }) => {
  // Fire user-prompt-submit only once per user message (it can surface on both
  // message.updated and message.part.updated).
  const seenUserMessages = new Set<string>()
  // message.* events don't carry the session id, so track it from events that do.
  let currentSessionID: string | null = null
  // The model of the most recent assistant message, forwarded for context.
  let currentModel: string | null = null
  const messageStore = new Map<string, any>()

  // Wrap in `sh -c` with a guard so a missing `ao` binary is a silent no-op
  // rather than a per-event error in the user's session.
  function hookCmd(hookName: string): string[] {
    return ["sh", "-c", `if ! command -v ao >/dev/null 2>&1; then exit 0; fi; exec ao hooks opencode ${hookName}`]
  }

  // Async invocation: fire-and-forget for events that don't precede process exit.
  async function callHook(hookName: string, payload: Record<string, unknown>) {
    try {
      const proc = Bun.spawn(hookCmd(hookName), {
        cwd: directory,
        stdin: new Blob([JSON.stringify(payload) + "\n"]),
        stdout: "ignore",
        stderr: "ignore",
      })
      await proc.exited
    } catch {
      // Best-effort: never let a reporting failure surface to opencode.
    }
  }

  // Sync invocation: `opencode run` exits on the idle event, so an async stop
  // hook would be killed before completing. user-prompt-submit is sync so AO
  // sees an ACTIVE session before any fast mid-turn work.
  function callHookSync(hookName: string, payload: Record<string, unknown>) {
    try {
      Bun.spawnSync(hookCmd(hookName), {
        cwd: directory,
        stdin: new TextEncoder().encode(JSON.stringify(payload) + "\n"),
        stdout: "ignore",
        stderr: "ignore",
      })
    } catch {
      // Best-effort: never let a reporting failure surface to opencode.
    }
  }

  function switchedSession(sessionID: string): boolean {
    if (currentSessionID === sessionID) return false
    seenUserMessages.clear()
    messageStore.clear()
    currentModel = null
    currentSessionID = sessionID
    return true
  }

  function reportUserPrompt(sessionID: string, messageID: string, prompt: string) {
    if (seenUserMessages.has(messageID)) return
    seenUserMessages.add(messageID)
    callHookSync("user-prompt-submit", { session_id: sessionID, prompt, model: currentModel ?? "" })
  }

  return {
    event: async ({ event }) => {
      try {
        switch (event.type) {
          case "session.created": {
            const session = (event as any).properties?.info
            if (!session?.id) break
            if (switchedSession(session.id)) {
              await callHook("session-start", { session_id: session.id })
            }
            break
          }

          case "message.updated": {
            const msg = (event as any).properties?.info
            if (!msg) break
            if (msg.sessionID && switchedSession(msg.sessionID)) {
              callHookSync("session-start", { session_id: msg.sessionID })
            }
            messageStore.set(msg.id, msg)
            if (msg.role === "assistant" && msg.modelID) currentModel = msg.modelID
            // Fallback: some `opencode run` flows never deliver message.part.updated
            // for the prompt, so start the turn from the user message itself.
            if (msg.role === "user") {
              const sessionID = msg.sessionID ?? currentSessionID
              if (sessionID) reportUserPrompt(sessionID, msg.id, "")
            }
            break
          }

          case "message.part.updated": {
            const part = (event as any).properties?.part
            if (!part?.messageID) break
            const msg = messageStore.get(part.messageID)
            if (msg?.role === "user" && part.type === "text") {
              const sessionID = msg.sessionID ?? currentSessionID
              if (sessionID) reportUserPrompt(sessionID, msg.id, part.text ?? "")
            }
            break
          }

          case "session.status": {
            // session.status fires in both TUI and `opencode run`; session.idle
            // is deprecated and not reliably emitted in run mode.
            const props = (event as any).properties
            if (props?.status?.type !== "idle") break
            const sessionID = props?.sessionID ?? currentSessionID
            if (!sessionID) break
            callHookSync("stop", { session_id: sessionID, model: currentModel ?? "" })
            break
          }
        }
      } catch {
        // Best-effort: never let a reporting failure surface to opencode.
      }
    },
  }
}
