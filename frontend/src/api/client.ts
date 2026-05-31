import createClient from "openapi-fetch";
import type { paths } from "./schema";

// Typed client for the daemon's loopback /api/v1 surface. Request and response
// types come from ./schema.d.ts, which is generated from the backend OpenAPI
// document (`npm run gen:api`) — never hand-maintained.
export const api = createClient<paths>({ baseUrl: "http://127.0.0.1:3001" });

// Re-export the generated component schemas for convenient use in the renderer,
// e.g. `components["schemas"]["Project"]`, `Summary`, `APIError`.
export type { components, paths } from "./schema";
