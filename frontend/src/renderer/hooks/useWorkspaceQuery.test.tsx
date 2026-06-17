import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
}));

import { useWorkspaceQuery } from "./useWorkspaceQuery";

function wrapper({ children }: { children: ReactNode }) {
	// The hook pins its own retry policy; retryDelay 0 keeps the error tests fast.
	const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

function respondWith(payload: {
	projects?: { data?: unknown; error?: unknown };
	sessions?: { data?: unknown; error?: unknown };
	prsBySession?: Record<string, { data?: unknown; error?: unknown }>;
}) {
	getMock.mockImplementation(async (url: string, options?: { params?: { path?: { sessionId?: string } } }) => {
		if (url === "/api/v1/projects") return payload.projects ?? { data: { projects: [] }, error: undefined };
		if (url === "/api/v1/sessions") return payload.sessions ?? { data: { sessions: [] }, error: undefined };
		if (url === "/api/v1/sessions/{sessionId}/pr") {
			const sessionId = options?.params?.path?.sessionId ?? "";
			return payload.prsBySession?.[sessionId] ?? { data: { sessionId, prs: [] }, error: undefined };
		}
		throw new Error(`unexpected GET ${url}`);
	});
}

beforeEach(() => {
	getMock.mockReset();
});

describe("useWorkspaceQuery", () => {
	it("maps projects and their sessions, applying provider/status/title fallbacks", async () => {
		respondWith({
			projects: {
				data: { projects: [{ id: "proj-1", name: "my-app", path: "/home/me/my-app" }] },
				error: undefined,
			},
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							terminalHandleId: "term-1",
							displayName: "fix-bug",
							harness: "claude-code",
							status: "mergeable",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
						{
							// Unknown harness/status and no displayName/issueId: falls back
							// to codex / working / the session id.
							id: "sess-2",
							projectId: "proj-1",
							harness: "mystery-agent",
							status: "bogus",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
						// Belongs to another project; must not leak into proj-1.
						{ id: "sess-3", projectId: "proj-2", isTerminated: false, updatedAt: "2026-06-10T16:15:04Z" },
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		const [workspace] = result.current.data ?? [];
		expect(workspace).toMatchObject({ id: "proj-1", name: "my-app", path: "/home/me/my-app" });
		expect(workspace.sessions).toHaveLength(2);
		expect(workspace.sessions[0]).toMatchObject({
			id: "sess-1",
			terminalHandleId: "term-1",
			title: "fix-bug",
			provider: "claude-code",
			status: "mergeable",
		});
		expect(workspace.sessions[1]).toMatchObject({
			id: "sess-2",
			title: "sess-2",
			provider: "codex",
			status: "working",
		});
	});

	it("hydrates each session's pullRequest from the /pr endpoint (issue #251)", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "pr_open",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
						{
							id: "sess-2",
							projectId: "proj-1",
							status: "working",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
			prsBySession: {
				"sess-1": {
					data: {
						sessionId: "sess-1",
						prs: [
							{
								number: 278,
								state: "open",
								url: "u",
								ci: "passing",
								review: "approved",
								mergeability: "clean",
								reviewComments: false,
								updatedAt: "2026-06-10T16:15:04Z",
							},
						],
					},
					error: undefined,
				},
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		const sessions = result.current.data?.[0].sessions ?? [];
		expect(sessions[0].pullRequest).toEqual({ number: 278, state: "open" });
		// No PR for the endpoint's empty response → undefined, so the empty states render.
		expect(sessions[1].pullRequest).toBeUndefined();
	});

	it("treats a per-session PR fetch error as no PR without failing the query", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "pr_open",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
			prsBySession: { "sess-1": { data: undefined, error: new Error("pr backend down") } },
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.[0].sessions[0].pullRequest).toBeUndefined();
	});

	it("skips the PR fetch for terminated sessions", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "merged",
							isTerminated: true,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(getMock).not.toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/pr", expect.anything());
		expect(result.current.data?.[0].sessions[0].pullRequest).toBeUndefined();
	});

	it("marks terminated sessions regardless of their reported status", async () => {
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "working",
							isTerminated: true,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			},
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.[0].sessions[0].status).toBe("terminated");
	});

	it("surfaces a projects fetch error", async () => {
		const failure = new TypeError("Failed to fetch");
		respondWith({ projects: { data: undefined, error: failure } });

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 3_000 });
		expect(result.current.error).toBe(failure);
	});

	it("surfaces a sessions fetch error even when projects load", async () => {
		const failure = new Error("sessions backend down");
		respondWith({
			projects: { data: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] }, error: undefined },
			sessions: { data: undefined, error: failure },
		});

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 3_000 });
		expect(result.current.error).toBe(failure);
	});
});
