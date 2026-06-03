# GORM vs sqlc for the Go + SQLite Agent-Orchestrator Backend

The agent-orchestrator — many concurrent agent sessions, event streams, job queues, and a hot mixed read/write path against a single SQLite file — is a workload where correctness under refactoring, predictable performance under concurrency, and tight control over the single SQLite writer matter more than CRUD velocity. On those axes **sqlc (compile-time codegen from raw SQL) is the better default**, because it pushes schema/query errors to build time, generates near-`database/sql`-speed code with no reflection, and hands you the raw `*sql.DB` so you can enforce the WAL + single-writer pool pattern. **GORM (runtime reflection ORM)** wins decisively on developer velocity and dynamic query building, but its strongest features (dynamic `Where`, associations, `AutoMigrate`, hooks) come paired with runtime-only error surfacing and several silent-correctness footguns (zero-value updates dropped, soft-delete + unique-index collisions, accidental N+1) that bite hardest in exactly this write-heavy, concurrency-sensitive system. sqlc's real costs are a less-mature SQLite engine (Beta) and the inability to build dynamic queries at runtime — both manageable with a small `database/sql`/sqlx escape hatch for the handful of genuinely dynamic queue/filter endpoints. The recommended shape is **sqlc for the static hot paths + a thin sqlx/raw-SQL layer for dynamic queries**, with migrations on golang-migrate or goose.

| Dimension | GORM | sqlc | Edge |
|---|---|---|---|
| Model | Runtime, reflection-based ORM | Build-time codegen from raw SQL | — |
| Type safety | Mostly runtime (string clauses opaque) | Compile/generate-time validated | **sqlc** |
| Performance | ~2–5x slower than raw, more allocs (directional) | ~`database/sql` baseline | **sqlc** |
| Dynamic queries | Native, elegant fluent builder | Not supported at runtime; needs workarounds | **GORM** |
| Batch insert ergonomics | `CreateInBatches` first-class | Loop-in-transaction or hand-rolled multi-VALUES | **GORM** |
| SQLite engine maturity | First-class dialect, mature drivers | **Beta** engine, balky parser | **GORM** |
| Migrations | `AutoMigrate` (not prod-grade) + external | None by design; pair with external tool | wash |
| Refactoring safety | Silent runtime drift | Breaks loudly at generate/compile | **sqlc** |
| Testing | In-memory SQLite good; sqlmock brittle; no free mock seam | Generated `Querier` interface; high-fidelity real-DB | **sqlc** |
| Single-writer control | `db.DB()` reachable, but implicit txns hold locks longer | Direct `*sql.DB`, explicit short txns | **sqlc** |
| GitHub stars (2026-05) | ~39.8k | ~17.8k | GORM (popularity) |

---

## 1. Philosophy & approach

The two tools sit on opposite sides of one question: is the Go struct or the SQL the source of truth?

**GORM — the database is a projection of your Go structs.** You declare a struct, and GORM derives table names, snake_case columns, primary keys, and associations via **runtime reflection**. Queries are assembled dynamically on every call through a fluent, chained API (`db.Where("status = ?", "running").Find(&sessions)`), with SQL rendered through a callback pipeline that inspects struct tags and field types via `reflect`. The mental model is object-centric: models, associations, hooks (`BeforeCreate`/`AfterSave`), and `AutoMigrate` that pushes struct shape into DDL. The DB schema is conceptually downstream of the Go code. You write structs + tags + method chains, mostly no `.sql` files; the cost is paid at runtime and the column/join behavior is implicit.

**sqlc — Go is a projection of your SQL.** You write a schema source (DDL) and a `query.sql` of annotated named queries:

```sql
-- name: GetRunningSessions :many
SELECT id, agent_name, status, created_at
FROM sessions WHERE status = ?;
```

At build time, `sqlc generate` parses the schema and each query with a real SQL parser, infers parameter/result types, and emits plain Go — a `Queries` struct with one typed method per query plus model structs. There is **no reflection and no runtime SQL building**; generated code is hand-written-quality `database/sql` (or pgx). The mental model is SQL-centric. The "ORM" disappears: no associations, no hooks, no lazy loading — just functions that run the SQL you wrote, committed and reviewable in the repo.

**The fork for this project:** GORM optimizes for abstraction and velocity (good when the model is in flux); sqlc optimizes for transparency and control (good for a high-throughput read/write system where you hand-tune SQL and use SQLite features). The line blurs slightly — GORM also exposes raw SQL and a builder, and GORM Gen/CLI now do codegen for type-safe DAOs — but *idiomatic* GORM is reflection-driven and *idiomatic* sqlc is codegen-driven. Notably, if you adopt GORM Gen you are already buying into codegen, at which point sqlc's SQL-first model is the cleaner version of that bet for this workload.

---

## 2. SQLite-specific support and quirks

### Drivers — both ecosystems route to the same two engines

| | cgo `mattn/go-sqlite3` | pure-Go `modernc.org/sqlite` |
|---|---|---|
| Activity | ~8.3k stars, actively maintained | cznic (GitLab-hosted), actively maintained, machine-transpiled SQLite C port |
| Build | requires CGO + C toolchain; cross-compile pain | no CGO, trivial static binaries |
| Speed | fastest, closest to native C | very close; competitive |
| FTS5 / JSON1 | **not compiled in by default** — needs `-tags "sqlite_fts5"` / `sqlite_json` | **included by default** |

- **sqlc** officially lists both drivers (both Beta) and is driver-agnostic at runtime — you pick in your own `sql.Open`.
- **GORM**'s official `gorm.io/driver/sqlite` wraps mattn (CGO required). The pure-Go path depends on the community `glebarez/sqlite` shim (~362 stars, push cadence slowed to May 2025). So GORM's pure-Go option leans on a smaller, less-active third party.

For an orchestrator you'll want as a single static binary, the **pure-Go (modernc) path is attractive**, and FTS5/JSON1 compiled-in-by-default removes a whole class of build-tag gotchas. sqlc supports modernc directly; GORM only via the slower-moving shim.

### FTS5 and JSON1: neither tool models them natively

Both work at runtime, but **neither GORM nor sqlc abstracts FTS5**. With GORM you drop to `db.Exec`/`db.Raw` for `CREATE VIRTUAL TABLE ... USING fts5` and `MATCH` queries — no ergonomic gain. With sqlc the risk is the **parser**: FTS5 DDL and some constructs have historically tripped sqlc's SQLite parser (e.g., issues #3739 ORDER BY expression parser error, #1733 parser limitations), so keep FTS5 DDL in migration-only files sqlc doesn't generate from, and hand-write those few accessors. JSON1 (`json_extract`, `->>`) is just function calls in your SQL for sqlc (you scan-and-unmarshal yourself); GORM offers a thin `datatypes.JSONQuery` helper but complex filtering falls back to raw SQL anyway.

### WAL, busy_timeout, and the single-writer model (the load-bearing concern)

This is a **SQLite-level** concern and is **orthogonal to GORM vs sqlc** — the controls live in the DSN/PRAGMAs and `database/sql` pool settings, configured identically for both. SQLite allows exactly **one writer at a time** regardless of library; a second writer gets `SQLITE_BUSY`. The standard mitigation:

- **WAL mode** (`journal_mode=WAL`) — one writer concurrent with many readers.
- **busy_timeout** (e.g. 5000ms) — a blocked writer waits/retries instead of erroring.
- **Serialize writers**: `db.SetMaxOpenConns(1)` on the write path, often a split design — a single-conn writer pool plus a multi-conn read-only pool (`mode=ro`).
- **Use `BEGIN IMMEDIATE` for write transactions.** busy_timeout alone does *not* cure read-to-write upgrade deadlocks: two DEFERRED transactions that each start as readers then try to upgrade can deadlock and return the non-retriable `SQLITE_BUSY_SNAPSHOT`. Starting write transactions as IMMEDIATE avoids this. Neither GORM nor sqlc does this for you.

Where the tools differ is in how easily you apply this pattern. **sqlc** generates plain `database/sql` calls, so you own the `*sql.DB` directly — the split-pool and IMMEDIATE patterns are natural and you can reason about which statement runs on which connection. **GORM** keeps a long-lived `*gorm.DB`; you reach the pool via `db.DB()`, but its implicit transactions (default-on for writes), hooks, association cascades, and prepared-statement cache add a layer where lock contention is harder to reason about, and "database is locked" reports under GORM+SQLite are common (root cause almost always the pool/WAL config). For a lock-sensitive orchestrator, sqlc's transparency is a genuine edge.

### Engine maturity and `ALTER TABLE`

sqlc treats SQLite as **Beta** (PostgreSQL/MySQL are mature); its parser lags real SQLite syntax, and type inference is weaker because of SQLite's dynamic typing — expect more `interface{}`/`sql.Null*` and the occasional `CAST(...)` hint. GORM treats SQLite as a first-class dialect, but its `AutoMigrate` is especially weak on SQLite because SQLite's `ALTER TABLE` is limited (GORM emulates some changes by table-copy; type/constraint changes are unreliable). Net: sqlc's SQLite risk is at build time (parser/inference); GORM's is at migration time.

---

## 3. Type safety, ergonomics, and the refactoring story

**sqlc — errors surface at build time.** Because queries are parsed and type-checked against the schema during `sqlc generate`, a renamed column, wrong arity, or type mismatch **fails generation or compilation**, not production. Results bind to generated structs, so IDE autocomplete and `go vet` cover your data layer. The refactoring loop is: change schema → `sqlc generate` → compiler flags every broken call site. For a system meant to evolve (new event types, queue columns), this "breaks loudly" property is the single biggest safety win.

**GORM — flexibility paid for in runtime drift.** Struct-tag mapping is convenient, but large parts of the API take **strings**: `Where("statuss = ?")`, `Order("craeted_at")`, `Select("...")`, `Pluck("col", ...)`. Typos and renames compile fine and fail (or silently return wrong/empty data) at runtime. There's no generate step to catch schema/code drift; `AutoMigrate` can even mask drift by reshaping the DB to match the structs. GORM Gen recovers some compile-time safety by generating typed DAOs, but that's opting into the codegen model sqlc gives you natively.

**Ergonomics trade:** GORM writes less code for CRUD and dynamic filters; sqlc writes explicit SQL but yields total clarity on what runs. For a team that values "the compiler is my migration checklist," sqlc wins; for rapid model churn with many ad-hoc queries, GORM is faster to move in.

---

## 4. Performance

**Directional, not gospel** (benchmarks vary wildly by workload, and the SQLite writer is usually the real ceiling):

- **sqlc** generates straight-line `database/sql` calls — **no reflection**, allocations close to hand-written code, baseline driver speed. Prepared statements are explicit and cacheable.
- **GORM** adds reflection, a callback pipeline, and dynamic SQL building per call. Community microbenchmarks put it on the order of **~2–5× slower with more allocations than raw `database/sql`** on hot paths (directional — some workloads show less). GORM supports `PrepareStmt: true` to cache statements and reduce parse overhead, narrowing the gap.

For this orchestrator, **SQLite's single-writer serialization dominates write throughput** far more than ORM CPU. Where sqlc's lower overhead matters is the **high-frequency read path** (polling sessions, draining event streams) and predictable GC behavior under load. Net: sqlc has the edge, but for many endpoints the difference is dwarfed by disk and lock contention.

---

## 5. Migration tooling and schema evolution

**sqlc does not do migrations — by design.** It only *reads* a schema (a `schema` path that can point at your migration files or a dedicated DDL file) to type-check queries; it never applies DDL. **Confirmed:** you pair it with an external migration tool. It understands the up-migrations of **golang-migrate, goose, and atlas** directly as schema input, so the same files drive both migration and codegen.

**Typical sqlc workflow:**
1. Write a new migration (golang-migrate/goose).
2. Point sqlc's `schema` at the migrations dir (or maintain `schema.sql`).
3. `sqlc generate`.
4. Compiler flags any query/call-site now invalid.
5. Apply the migration at deploy via the migration tool.

**GORM** ships **`AutoMigrate`**, which reflects structs and creates tables/missing columns/indexes. Convenient for prototyping, but **not production-grade**: it **won't drop columns, won't rename safely (rename = add new + leave old), and has no down-migrations or destructive-change control.** Real GORM deployments therefore **also** adopt golang-migrate/goose/atlas — so migrations end up an external concern for *both* tools. The difference: sqlc forces the explicit, versioned workflow from day one; GORM tempts you with `AutoMigrate` and you discover its limits later.

---

## 6. Testing story

**Both favor real in-memory SQLite** for high-fidelity tests — fast, real SQL semantics. Use a shared-cache DSN (`file::memory:?cache=shared`) or a single connection, because each `:memory:` connection otherwise gets its **own** database (a classic surprise in concurrent test setups).

- **sqlc** generates a **`Querier` interface** plus a concrete `*Queries`. That interface is a free mocking seam for unit-testing business logic, while integration tests run the same generated code against in-memory SQLite. High-fidelity and naturally testable.
- **GORM** has **no built-in interface seam**; `go-sqlmock` exists but is **brittle** against GORM's dynamically generated SQL (you assert on SQL strings GORM may change between versions). The pragmatic path is real SQLite + fixtures, accepting that pure unit isolation is harder.

For a queue/event system where you want to test claim/transition logic precisely, sqlc's interface + real-DB combination is the stronger story.

---

## 7. Code generation / build pipeline implications (sqlc)

Adopting sqlc introduces a **codegen step** into the build:

- **Inputs:** `sqlc.yaml` (engine: `sqlite`), a schema source, and `query.sql` files with `-- name: X :one|:many|:exec` annotations.
- **Output:** committed generated Go (`models.go`, `query.sql.go`, `querier.go`) — checked into the repo.
- **Regeneration discipline:** every schema/query change requires `sqlc generate`; forgetting it, or letting the schema input drift from actual migrations, is the **main friction point**. Mitigate with a **CI drift check** (`sqlc generate` then fail if `git diff` is non-empty) plus **`sqlc vet`** for lint rules.
- **Build-breakage is a feature:** an invalid query won't generate, so bad SQL never reaches runtime.
- **Tooling cost:** contributors and CI need the pinned `sqlc` binary; `:memory:`-style local iteration is unaffected.

**GORM has no codegen step** (unless you adopt GORM Gen), which is less upfront friction but moves error detection to runtime. The sqlc pipeline cost is real but small, and it buys the compile-time guarantees above.

---

## 8. Community health, maintenance, known footguns

**Both are healthy and actively maintained (2026):**

| | GORM | sqlc |
|---|---|---|
| Stars (2026-05) | ~39.8k | ~17.8k |
| Position | Most popular Go ORM | Leading SQL-first codegen tool |
| Releases | Frequent, stable v2 | Frequent; SQLite engine still **Beta** |

**GORM footguns (all confirmed, all relevant here):**
- **Zero-value updates silently dropped.** `db.Model(&s).Updates(Session{Status: "", Retries: 0})` **skips** zero-valued struct fields (`""`, `0`, `false`), so setting a column *to* its zero value does nothing. You must use a `map[string]interface{}` or `Select("col")` to force it. A live correctness bug magnet for status/counter fields.
- **Soft delete (`gorm.DeletedAt`)** silently adds `WHERE deleted_at IS NULL` to every query. Surprises ("my rows vanished"); requires `Unscoped()` to see/really-delete. Worse, **soft-deleted rows still occupy unique indexes**, so re-inserting a "deleted" natural key throws a uniqueness violation — a real schema-design trap.
- **N+1 — with nuance.** Naively walking associations issues a query per parent. But GORM's **`Preload` batches related rows into a single additional `IN (...)` query (1 + 1, not 1 + N)** — so eager-loading is fine; the footgun is *forgetting* to Preload/Joins and lazily accessing in a loop.
- **Hooks & default transactions** add implicit behavior and hold write locks longer (see §2).

**sqlc footguns:**
- **No runtime-dynamic SQL.** Generated queries are static. **Nuance:** a variable-length `IN (...)` is *partly* solvable — sqlc supports `sqlc.slice()` for several engines/drivers to expand a slice param, and on SQLite a `json_each(?)` array param works. What remains genuinely hard is **runtime-composed `WHERE`/sort/optional filters** (search endpoints), which need per-shape queries or a raw-SQL fallback.
- **SQLite engine is Beta** and its parser rejects some valid SQL (FTS5 DDL, certain `ORDER BY` expressions, window/CTE edge cases — see issues #3739, #1733). Keep unsupported DDL out of the generate path.
- Less convenient for sprawling ad-hoc query shapes.

---

## 9. Best fit for the agent-orchestrator workload

The workload — **many concurrent sessions, event streams, job queues, heavy mixed reads/writes on one SQLite file** — breaks down as:

**Where sqlc fits well:**
- **Job-queue claim/transition** logic (`UPDATE ... WHERE status='queued' ... RETURNING`, atomic state machines) benefits from **explicit, reviewable, tunable SQL** and short, controllable transactions (`BEGIN IMMEDIATE`) over a raw `*sql.DB`.
- **Event-stream appends** are static high-frequency inserts — sqlc's low overhead and prepared statements shine; batch via a transaction loop or a generated multi-VALUES query.
- **Hot read paths** (poll session state, tail events) get baseline `database/sql` speed and predictable GC.
- **Refactoring safety** as the event/queue schema grows — compile-time breakage is worth a lot in a system of record.

**Where you'll feel sqlc's limits:**
- **Dynamic list/filter endpoints** (sessions by optional status/agent/time range, sortable) — sqlc can't compose these at runtime. This is the one area to carve out.
- **Batch insert ergonomics** are nicer in GORM (`CreateInBatches`).

**The pragmatic architecture:** **sqlc for the static hot paths** (queue ops, event appends, keyed lookups, state transitions) **+ a thin `sqlx`/`database/sql` layer for the handful of dynamic query/filter endpoints**, all over **one shared, WAL-mode, single-writer-pool `*sql.DB`**. This keeps correctness and speed where they matter and dynamism where you need it — without paying GORM's runtime-drift and footgun tax across the whole codebase.

GORM would suit this workload if the priority were **velocity and pervasive dynamic queries** over compile-time safety — e.g., an early-stage prototype with a churning schema and a small team that values `AutoMigrate` and fluent filters more than predictable performance and loud refactors.

---

## 10. Alternatives worth flagging (brief)

- **sqlx** — thin extension of `database/sql` (named params, `StructScan`); **no codegen, no ORM, fully dynamic**. Best as the **dynamic-query companion to sqlc**: hand-written SQL, runtime flexibility, zero build step — at the cost of sqlc's compile-time checking. Strong fit for the filter endpoints above.
- **ent** (Meta, entgo.io) — schema-as-Go-code **graph ORM** with heavy codegen, type-safe traversals, hooks, and strong support for complex relations. Powerful but opinionated and heavier; shines when the domain is deeply relational/graph-like. SQLite supported.
- **bun** — lightweight SQL-first ORM (successor to go-pg): fluent, type-aware query builder, migrations, good performance, multi-DB incl. SQLite. A **middle ground** between GORM's convenience and sqlc's explicitness if you want one tool with a builder + migrations and some dynamism.
- **jet** — codegen like sqlc, but generates a **type-safe query *builder*** from the live DB schema rather than from hand-written SQL — giving **compile-time-checked *dynamic* queries**. Directly targets sqlc's weak spot; evaluate it if dynamic-but-type-safe matters more than committing raw SQL.

Keep focus on GORM vs sqlc; the above are escape hatches/contenders, not the main decision.

---

## 11. Recommendation and rationale

**Adopt sqlc as the primary data layer**, paired with **golang-migrate or goose** for migrations and a **thin sqlx/`database/sql` escape hatch** for genuinely dynamic queries. Use the **pure-Go `modernc.org/sqlite`** driver (no CGO, FTS5/JSON1 built in) over **one shared `*sql.DB`** configured for **WAL + busy_timeout + single-writer pool + `BEGIN IMMEDIATE`** writes.

**Why sqlc wins for this project:**
1. **Compile-time safety on an evolving schema** — queue/event schemas will change; sqlc turns drift into build errors, not 2 a.m. incidents.
2. **Explicit, tunable SQL + low overhead** — ideal for queue claims, event appends, and hot reads where you want to see and control exactly what hits the single SQLite writer.
3. **Clean testing seam** — generated `Querier` interface + in-memory SQLite.
4. **Sidesteps GORM's footguns** (zero-value updates, soft-delete/unique-index traps, accidental N+1, lock-holding implicit txns) that are most dangerous in a write-heavy concurrent system.
5. **Operational fit** — raw `*sql.DB` makes the mandatory SQLite single-writer discipline natural; pure-Go driver yields a clean static binary.

**Accept these costs:** a codegen step (mitigated by CI drift-check + `sqlc vet`), a Beta SQLite engine (keep exotic DDL out of the generate path), and writing dynamic queries by hand in the sqlx layer.

**Choose GORM instead if:** the team prioritizes raw development speed and pervasive dynamic queries over compile-time guarantees, the schema is highly volatile, or CRUD-heavy breadth matters more than hot-path control. **Choose ent** for a deeply relational/graph domain; **bun** if you want one SQL-first tool with builder + migrations and moderate dynamism; **jet** if you specifically need type-safe *dynamic* queries (sqlc's main gap).

Whatever you pick, **the decisive lever is SQLite tuning — WAL + busy_timeout + single writer connection + BEGIN IMMEDIATE — which matters more than the ORM choice itself.**

---

## TL;DR

**Use sqlc** for the Go + SQLite agent-orchestrator: compile-time-checked, explicit, low-overhead SQL is the right fit for a job-queue/event-stream core with an evolving schema, and its generated `Querier` interface makes testing clean. Pair it with **golang-migrate/goose** for migrations (sqlc does none by design) and a **thin sqlx/`database/sql` layer** for the few genuinely dynamic filter queries (sqlc's one real weakness). Prefer the **pure-Go `modernc.org/sqlite`** driver. Reach for **GORM** only if velocity and pervasive dynamic queries outweigh compile-time safety; consider **ent** (graph-heavy), **bun** (SQL-first all-in-one), or **jet** (type-safe dynamic queries) at the margins. Above all, **tune SQLite: WAL mode + busy_timeout + a single writer connection + BEGIN IMMEDIATE** — that decision outweighs the ORM itself.

## References

- GORM docs — https://gorm.io/docs/
- GORM GitHub — https://github.com/go-gorm/gorm
- GORM SQLite driver — https://github.com/go-gorm/sqlite
- glebarez/sqlite (pure-Go GORM driver) — https://github.com/glebarez/sqlite
- sqlc docs — https://docs.sqlc.dev/
- sqlc GitHub — https://github.com/sqlc-dev/sqlc
- sqlc SQLite engine status & issues — https://github.com/sqlc-dev/sqlc/issues/3739 , https://github.com/sqlc-dev/sqlc/issues/1733
- mattn/go-sqlite3 — https://github.com/mattn/go-sqlite3
- modernc.org/sqlite — https://pkg.go.dev/modernc.org/sqlite
- golang-migrate — https://github.com/golang-migrate/migrate
- goose — https://github.com/pressly/goose
- atlas — https://atlasgo.io/
- sqlx — https://github.com/jmoiron/sqlx
- ent — https://entgo.io/
- bun — https://bun.uptrace.dev/
- jet — https://github.com/go-jet/jet
- SQLite WAL — https://www.sqlite.org/wal.html
- SQLite locking / BEGIN IMMEDIATE — https://www.sqlite.org/lang_transaction.html
