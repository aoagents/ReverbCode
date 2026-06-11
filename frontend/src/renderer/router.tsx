import { createHashHistory, createRouter } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import { routeTree } from "./routeTree.gen";

// Hash history is required for Electron's file:// renderer origin — browser
// history would break on hard reload since there is no server to serve paths.
export function createAppRouter(queryClient: QueryClient) {
	return createRouter({
		history: createHashHistory(),
		routeTree,
		context: { queryClient },
		defaultPreload: "intent",
		// Always re-run loaders when a route is preloaded or visited so React
		// Query's cache is the single source of truth for staleness.
		defaultPreloadStaleTime: 0,
		scrollRestoration: true,
	});
}
