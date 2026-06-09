import type { QueryClient } from "@tanstack/react-query";
import { aoBridge } from "./bridge";
import { getApiBaseUrl } from "./api-client";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";

export type EventTransport = {
  connect: () => () => void;
};

const INVALIDATE_DEBOUNCE_MS = 150;

// CDC event types the daemon pushes over the SSE stream (see
// backend/internal/cdc/event.go). The SSE writer tags each frame with
// `event: <type>`, so named events bypass EventSource.onmessage and must be
// subscribed explicitly. Every one of these can change the project/session list
// the sidebar renders, so they all trigger a (debounced) workspace refetch.
const CDC_EVENT_TYPES = [
  "session_created",
  "session_updated",
  "pr_created",
  "pr_updated",
  "pr_check_recorded",
  "pr_session_changed",
  "pr_review_thread_added",
  "pr_review_thread_resolved",
] as const;

/**
 * Wires live server state into the TanStack Query cache. Two sources feed it:
 *   - daemon lifecycle over Electron IPC (coming up/down changes session availability)
 *   - the backend CDC stream over SSE (project/session/PR changes)
 * Both invalidate the ["workspaces"] query so the UI refetches. Invalidations are
 * debounced because a single user action can emit a burst of CDC events.
 */
export function createEventTransport(queryClient: QueryClient): EventTransport {
  return {
    connect() {
      let debounce: ReturnType<typeof setTimeout> | undefined;
      let source: EventSource | undefined;
      let sourceBaseUrl: string | undefined;
      const refreshWorkspaces = () => {
        if (debounce) clearTimeout(debounce);
        debounce = setTimeout(() => {
          void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
        }, INVALIDATE_DEBOUNCE_MS);
      };

      const connectSource = () => {
        // EventSource is unavailable in jsdom (tests) and some preview surfaces; guard it.
        if (typeof EventSource === "undefined") return;
        const baseUrl = getApiBaseUrl();
        if (source && sourceBaseUrl === baseUrl) return;
        source?.close();
        source = undefined;
        sourceBaseUrl = baseUrl;
        try {
          source = new EventSource(`${baseUrl.replace(/\/+$/, "")}/api/v1/events`);
          source.onmessage = refreshWorkspaces; // unnamed events, if any
          for (const type of CDC_EVENT_TYPES) {
            source.addEventListener(type, refreshWorkspaces);
          }
          // EventSource auto-reconnects and resumes via Last-Event-ID; no handler needed.
        } catch {
          source = undefined;
        }
      };

      const removeDaemonListener = aoBridge.daemon.onStatus(() => {
        connectSource();
        refreshWorkspaces();
      });
      connectSource();

      return () => {
        if (debounce) clearTimeout(debounce);
        removeDaemonListener();
        source?.close();
      };
    },
  };
}
