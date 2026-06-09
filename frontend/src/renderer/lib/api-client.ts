import createClient from "openapi-fetch";
import type { paths } from "../../api/schema";

const apiBaseUrl = import.meta.env.VITE_AO_API_BASE_URL ?? "http://127.0.0.1:4317";

export const apiClient = createClient<paths>({
  baseUrl: `${apiBaseUrl}/api/v1`,
});
