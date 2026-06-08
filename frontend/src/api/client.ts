import createClient from "openapi-fetch";
import type { paths } from "./schema";

// The daemon binds DEFAULT_PORT unless AO_PORT overrides it (see config.go),
// and writes the port it actually bound to running.json (runfile.File.Port).
// DEFAULT_PORT only mirrors config.DefaultPort as a last-resort fallback for
// when the runfile hasn't been read yet — clients must NOT assume it.
export const DEFAULT_PORT = 3001;

// baseURLForPort builds the loopback base URL for a known daemon port. Pass the
// port read from running.json; default only as a fallback.
export const baseURLForPort = (port: number = DEFAULT_PORT): string =>
  `http://127.0.0.1:${port}`;

// createApiClient builds a typed openapi-fetch client for the daemon's
// /api/v1 surface. The renderer cannot read running.json directly, so the
// wiring layer must read it (via IPC to the main process) and pass the resolved
// base URL here — hardcoding DEFAULT_PORT would silently break any user who
// overrides AO_PORT. Request/response types come from ./schema.ts, generated
// from the backend OpenAPI document (`npm run api` at the repo root).
export const createApiClient = (baseUrl: string) =>
  createClient<paths>({ baseUrl });

// Re-export the generated component schemas for convenient use in the renderer,
// e.g. components["schemas"]["Project"], "Session", "APIError".
export type { components, paths } from "./schema";
