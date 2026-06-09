import { describe, expect, it } from "vitest";

import { router } from "../router";

// Phase 0 smoke test: the toolchain (Vite + Vitest + TanStack Router) is wired
// and the code-based route tree constructs without error. Behavioral UI tests
// arrive with the first real components.
describe("ao-web scaffolding", () => {
  it("constructs the TanStack Router instance", () => {
    expect(router).toBeDefined();
    expect(router.routeTree).toBeDefined();
  });

  it("registers the index route on the tree", () => {
    expect(router.routeTree.children).toBeDefined();
  });
});
