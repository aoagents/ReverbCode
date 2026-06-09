import createClient from "openapi-fetch";
import type { paths } from "../../api/schema";

function devApiBaseUrl(): string {
  return typeof window === "undefined" ? "http://127.0.0.1:3001" : window.location.origin;
}

const initialApiBaseUrl =
  import.meta.env.VITE_AO_API_BASE_URL ?? (import.meta.env.DEV ? devApiBaseUrl() : "http://127.0.0.1:3001");

let runtimeApiBaseUrl = initialApiBaseUrl;

export function getApiBaseUrl(): string {
  return runtimeApiBaseUrl;
}

export function setApiBaseUrl(nextBaseUrl: string): void {
  runtimeApiBaseUrl = nextBaseUrl.replace(/\/+$/, "");
}

async function runtimeFetch(input: Request): Promise<Response> {
  const baseUrl = getApiBaseUrl();
  if (!baseUrl) {
    return fetch(input);
  }

  const url = new URL(input.url);
  const target = new URL(url.pathname + url.search + url.hash, baseUrl);
  return fetch(new Request(target, input));
}

export const apiClient = createClient<paths>({
  baseUrl: initialApiBaseUrl,
  fetch: runtimeFetch,
});
