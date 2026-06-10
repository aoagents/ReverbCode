import createClient from "openapi-fetch";
import type { paths } from "../../api/schema";

function devApiBaseUrl(): string {
  return typeof window === "undefined" ? "http://127.0.0.1:3001" : window.location.origin;
}

const initialApiBaseUrl =
  import.meta.env.VITE_AO_API_BASE_URL ?? (import.meta.env.DEV ? devApiBaseUrl() : "http://127.0.0.1:3001");

let runtimeApiBaseUrl = initialApiBaseUrl;

const baseUrlListeners = new Set<() => void>();

export function getApiBaseUrl(): string {
  return runtimeApiBaseUrl;
}

/**
 * Subscribe to base-URL changes (useSyncExternalStore-compatible). Long-lived
 * connections bound to a specific port — the terminal mux WebSocket, the SSE
 * stream — use this to rebind when the daemon comes back on a different port.
 */
export function subscribeApiBaseUrl(listener: () => void): () => void {
  baseUrlListeners.add(listener);
  return () => {
    baseUrlListeners.delete(listener);
  };
}

export function setApiBaseUrl(nextBaseUrl: string): void {
  const normalized = nextBaseUrl.replace(/\/+$/, "");
  if (normalized === runtimeApiBaseUrl) return;
  runtimeApiBaseUrl = normalized;
  baseUrlListeners.forEach((listener) => listener());
}

async function runtimeFetch(input: Request): Promise<Response> {
  const baseUrl = getApiBaseUrl();
  if (!baseUrl) {
    return fetch(input);
  }

  const url = new URL(input.url);
  const target = new URL(url.pathname + url.search + url.hash, baseUrl);
  if (target.href === input.url) {
    return fetch(input);
  }

  // Rebase onto the runtime base URL by copying fields explicitly and
  // buffering the body. `new Request(target, input)` reads the source
  // request's `duplex` getter, which Electron's Chromium lacks — it throws
  // "The duplex member must be specified" for any request with a body, so
  // every POST would fail in the packaged app. API bodies are small JSON;
  // buffering sidesteps streaming-duplex semantics entirely.
  const body = input.method === "GET" || input.method === "HEAD" ? undefined : await input.arrayBuffer();
  return fetch(target, {
    method: input.method,
    headers: input.headers,
    body,
    signal: input.signal,
    credentials: input.credentials,
    cache: input.cache,
    redirect: input.redirect,
    referrerPolicy: input.referrerPolicy,
    integrity: input.integrity,
    keepalive: input.keepalive,
  });
}

export const apiClient = createClient<paths>({
  baseUrl: initialApiBaseUrl,
  fetch: runtimeFetch,
});
