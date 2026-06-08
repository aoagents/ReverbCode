# ReverbCode desktop — design language

The design system is **code, not prose**. The single source of truth is the
`@theme` block in [`src/renderer/styles/theme.css`](src/renderer/styles/theme.css).
Change a token there and it propagates everywhere — Tailwind utilities, shadcn
components, and the legacy plain-CSS components all read the same values.

If this doc and `theme.css` ever disagree, **`theme.css` wins.**

## How styling is layered

```
src/renderer/
  styles/theme.css   ← @theme tokens (THE design language) + Tailwind import
  styles.css         ← component CSS, consumes tokens via var(--bg) aliases
  components/ui/*     ← shadcn components, consume tokens via Tailwind utilities
```

- **Tailwind v4** is added via `@tailwindcss/vite`. Preflight (the CSS reset) is
  intentionally **not** imported, so the existing hand-written components keep
  their exact look. New components may use Tailwind utilities freely.
- Every `@theme` variable is emitted to `:root` and, when namespaced
  (`--color-*`, `--radius-*`, `--text-*`, `--font-*`), also generates a utility
  (`bg-card`, `text-muted`, `rounded-lg`, `font-mono`, …).
- Legacy short aliases (`--bg`, `--fg`, `--orange`, …) at the bottom of
  `theme.css` map onto the `@theme` tokens so older CSS needs no edits.

## Token groups (see `theme.css` for values)

| Group | Tokens | Use |
|---|---|---|
| Type | `--font-sans`, `--font-mono`, `--text-2xs…sm` | text |
| Surfaces | `--color-bg`, `--color-bar`, `--color-inset`, `--color-card`, `--color-card-hover`, `--color-elevated` | backgrounds, darkest → lightest |
| Text | `--color-fg`, `--color-muted`, `--color-dim` | primary / secondary / tertiary |
| Lines | `--color-border`, `--color-border-strong` | borders, warm low-alpha |
| Brand | `--color-primary`, `--color-primary-hover`, `--color-ring` | primary actions (periwinkle) |
| Status tones | `--color-working` (orange), `--color-needs` (amber), `--color-review` (periwinkle), `--color-merge` (green), `--color-danger` (red), `--color-merged` (purple), `--color-neutral` (dim) | board columns, badges, dots |
| Radius | `--radius-sm`, `--radius`, `--radius-lg`, `--radius-xl` | corners |

## Rules

1. **Never hard-code a colour/size in a component.** Reference a token (Tailwind
   utility, `--color-*`, or a legacy alias). If a value is missing, add a token.
2. **Status colour = meaning, not decoration.** `working`→orange, `needs`→amber,
   `review`→periwinkle, `merge`→green, `danger`→red. The board columns, card
   chips, and sidebar dots all derive from `STATUS_META` in `lib/api.ts`, which
   maps a session status to a tone + column.
3. **Surfaces stack by elevation:** `bg` (app) < `bar`/`inset` < `card` <
   `elevated`. Don't invent intermediate greys.
4. **Mono for data** (ids, branches, timestamps, terminal), sans for prose.

## Adding / changing a token

1. Edit the value (or add a new `--color-*` / `--radius-*` …) in the `@theme`
   block of `theme.css`.
2. If older plain-CSS needs the new token under a short name, add an alias in the
   `:root` block just below `@theme`.
3. That's it — no component edits needed for a value change.

> Values are currently hex for exact fidelity with the original screenshots.
> They can be migrated to `oklch(...)` (like emdash's `app.css`) token-by-token
> without touching any component, since everything reads the variable.
