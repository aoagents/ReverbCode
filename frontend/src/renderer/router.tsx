import { createHashHistory, createRootRoute, createRoute, createRouter, Outlet } from "@tanstack/react-router";
import { App } from "./App";

const rootRoute = createRootRoute({
  component: RootLayout,
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: App,
});

const workspaceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/workspaces/$workspaceId",
  component: WorkspaceRoute,
});

const sessionRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/workspaces/$workspaceId/sessions/$sessionId",
  component: SessionRoute,
});

const routeTree = rootRoute.addChildren([indexRoute, workspaceRoute, sessionRoute]);

export const router = createRouter({
  history: createHashHistory(),
  routeTree,
});

function RootLayout() {
  return <Outlet />;
}

function WorkspaceRoute() {
  const { workspaceId } = workspaceRoute.useParams();
  return <App routeWorkspaceId={workspaceId} />;
}

function SessionRoute() {
  const { sessionId, workspaceId } = sessionRoute.useParams();
  return <App routeSessionId={sessionId} routeWorkspaceId={workspaceId} />;
}

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
