import { createRootRouteWithContext, Outlet } from "@tanstack/react-router";
import { TooltipProvider } from "../components/ui/tooltip";
import type { QueryClient } from "@tanstack/react-query";

export const Route = createRootRouteWithContext<{
	queryClient: QueryClient;
}>()({
	component: RootComponent,
});

function RootComponent() {
	return (
		<TooltipProvider>
			<Outlet />
		</TooltipProvider>
	);
}
