import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
} from "@tanstack/react-router";

import { ScaffoldingLive } from "./App";

// Code-based route tree (see PR notes for why we chose code-based over the
// file-based router plugin). The tree is assembled explicitly here rather than
// generated from the filesystem, so there is no routeTree.gen.ts to commit and
// no build-time codegen step in the Vite pipeline.
const rootRoute = createRootRoute({
  component: Outlet,
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: ScaffoldingLive,
});

const routeTree = rootRoute.addChildren([indexRoute]);

export const router = createRouter({ routeTree });

// Register the router instance type for end-to-end type inference across the app.
declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
