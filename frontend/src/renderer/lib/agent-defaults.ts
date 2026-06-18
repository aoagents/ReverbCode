import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";

export type AgentDefaults = components["schemas"]["AgentDefaultsResponse"];
export type AgentDefaultsInput = components["schemas"]["AgentDefaultsRequest"];

export const agentDefaultsQueryKey = ["settings", "agents"] as const;

export async function fetchAgentDefaults(): Promise<AgentDefaults> {
	const { data, error } = await apiClient.GET("/api/v1/settings/agents");
	if (error) throw new Error(apiErrorMessage(error));
	if (!data) throw new Error("Agent defaults response was empty");
	return data;
}

export async function saveAgentDefaults(input: AgentDefaultsInput): Promise<AgentDefaults> {
	const { data, error } = await apiClient.PUT("/api/v1/settings/agents", { body: input });
	if (error) throw new Error(apiErrorMessage(error));
	if (!data) throw new Error("Agent defaults response was empty");
	return data;
}
