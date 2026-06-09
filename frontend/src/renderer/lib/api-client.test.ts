import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient, getApiBaseUrl, setApiBaseUrl } from "./api-client";

describe("apiClient runtime base URL", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    setApiBaseUrl("http://127.0.0.1:3001");
  });

  it("rewrites requests to the current runtime daemon port", async () => {
    const seenUrls: string[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL) => {
      seenUrls.push(input instanceof Request ? input.url : input.toString());
      return new Response(JSON.stringify({ projects: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });

    setApiBaseUrl("http://127.0.0.1:3037/");

    const { error } = await apiClient.GET("/api/v1/projects");

    expect(error).toBeUndefined();
    expect(getApiBaseUrl()).toBe("http://127.0.0.1:3037");
    expect(seenUrls).toEqual(["http://127.0.0.1:3037/api/v1/projects"]);
  });

  it("passes the request through untouched when the base URL is empty", async () => {
    const seen: Request[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL) => {
      seen.push(input as Request);
      return new Response(JSON.stringify({ projects: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });

    setApiBaseUrl("");

    const { error } = await apiClient.GET("/api/v1/projects");

    expect(error).toBeUndefined();
    expect(getApiBaseUrl()).toBe("");
    // Empty base → no rewrite; openapi-fetch's own request reaches fetch as-is.
    expect(seen).toHaveLength(1);
    expect(seen[0].url).toContain("/api/v1/projects");
  });
});
