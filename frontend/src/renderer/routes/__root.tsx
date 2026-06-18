import { createRootRouteWithContext, Outlet, useRouterState } from "@tanstack/react-router";
import { useEffect } from "react";
import { TooltipProvider } from "../components/ui/tooltip";
import type { QueryClient } from "@tanstack/react-query";
import { captureRendererEvent } from "../lib/telemetry";

export const Route = createRootRouteWithContext<{
	queryClient: QueryClient;
}>()({
	component: RootComponent,
});

function RootComponent() {
	const location = useRouterState({ select: (state) => state.location });

	useEffect(() => {
		void captureRendererEvent("ao.renderer.route_viewed", {
			pathname: location.pathname,
			search: location.searchStr,
			hash: window.location.hash,
		});
	}, [location.pathname, location.searchStr]);

	return (
		<TooltipProvider>
			<Outlet />
		</TooltipProvider>
	);
}
