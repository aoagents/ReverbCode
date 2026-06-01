# API contract — code-first OpenAPI

The `/api/v1` HTTP contract is **code-first**: the Go request/response types are
the source of truth, and `openapi.yaml` plus the frontend TypeScript types are
**generated** from them. You never hand-write the spec or the TS types.

```
 Go types + operation registry         openapi.yaml (generated)        consumers
 ─────────────────────────────         ────────────────────────        ─────────
 project.AddInput, Project, ...   ──►   apispec.Build()  ──►  cmd/genspec  ──►  go:embed  ──►  GET /api/v1/openapi.yaml
 httpx.Error, controllers.*Response     (swaggest reflect)    writes file       (apispec.go)    + frontend/src/api/schema.d.ts
        +                                                                                        (openapi-typescript)
 projectOperations() registry
```

## Where each piece lives

| Piece | Location |
|---|---|
| Request bodies / results | `backend/internal/project/dto.go` (`AddInput`, `UpdateConfigInput`, `RemoveResult`) |
| Response envelopes + path params | `backend/internal/httpd/controllers/dto.go` (`ListProjectsResponse`, `ProjectResponse`, `GetProjectResponse`, `ProjectOrDegraded`, `ProjectIDParam`) |
| Entities | `backend/internal/project/types.go` (`Project`, `Summary`, `Degraded`, config blobs) |
| Error envelope (`APIError`) | `backend/internal/httpd/httpx/httpx.go` (`Error`) |
| Operation registry + generator | `backend/internal/httpd/apispec/build.go` (`Build()`, `projectOperations()`) |
| Generator entrypoint | `backend/cmd/genspec/main.go` (run by `//go:generate`, see `apispec/gen.go`) |
| Generated spec (embedded + served) | `backend/internal/httpd/apispec/openapi.yaml` |
| Generated TS types + client | `frontend/src/api/schema.d.ts`, `frontend/src/api/client.ts` |

Schema facets are plain struct tags on those types — `description`, `enum`,
`default`; `required` is derived automatically from the absence of `,omitempty`.
The same Go types are used by the handlers (to decode/encode) **and** by
`apispec.Build()` (to reflect the schema), so the runtime and the spec can't
disagree.

## Process: changing or adding an API route

> **Rule:** edit Go, then regenerate. Never hand-edit `openapi.yaml` or
> `schema.d.ts` — they are generated artifacts.

1. **Edit the Go contract.**
   - *Change a field / shape:* edit the struct where it lives (see the table
     above) and adjust its tags. `required` follows `omitempty`.
   - *Add or remove a route:* do **both** —
     1. wire the handler in `controllers/projects.go` (`Register` + the handler
        func), and
     2. add/remove its entry in `projectOperations()` in `apispec/build.go`
        (method, path, `operationId`, summary, request struct(s), response
        struct per status code).

2. **Regenerate** (from `backend/`):
   ```bash
   go generate ./...                    # Go → openapi.yaml
   npm --prefix ../frontend run gen:api # openapi.yaml → frontend/src/api/schema.d.ts
   ```

3. **Verify:** `go test ./...`
   - `TestBuild_MatchesEmbedded` fails if you forgot to regenerate `openapi.yaml`.
   - `TestRouteSpecParity` fails if a mounted route has no spec operation (or a
     spec operation has no route).

4. **Commit** the Go change **together with** the regenerated `openapi.yaml` and
   `schema.d.ts`.

5. **CI** (`.github/workflows/go.yml`, job `gen-verify`) re-runs both generators
   and `git diff --exit-code`s — a stale artifact blocks the merge.

## Guarantees

- **No drift:** `go test` (locally) and CI both fail if the committed artifacts
  don't match a fresh generation.
- **Route ↔ spec parity:** every served `/api/v1` route has a spec operation and
  vice-versa (`parity_test.go`).
- **One definition per shape:** each wire type is declared once, in the package
  that uses it; `apispec/build.go` declares no wire types — only the registry.

> Out of scope here: runtime request/response **validation** against the spec
> (a middleware) is tracked separately (#19).
