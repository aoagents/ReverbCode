export type NotificationPriority = "urgent" | "action" | "warning" | "info";

export type NotificationActionKind =
  | "open-session"
  | "open-pr"
  | "open-review"
  | "open-ci"
  | "restore-session"
  | "send-message"
  | "merge-pr"
  | "mark-read"
  | "dismiss";

export interface NotificationAction {
  id: string;
  kind: NotificationActionKind;
  label: string;
  route?: string;
  url?: string;
}

export interface NotificationEventBody {
  id: string;
  type: string;
  priority: NotificationPriority;
  sessionId: string;
  projectId: string;
  timestamp: string;
  message: string;
  data: Record<string, unknown>;
}

export interface NotificationRecord {
  seq: number;
  id: string;
  receivedAt: string;
  readAt: string | null;
  archivedAt: string | null;
  event: NotificationEventBody;
  actions: NotificationAction[];
}

export interface NotificationListResponse {
  notifications: NotificationRecord[];
  unreadCount: number;
  limit: number;
  nextBeforeSeq: number | null;
}

export interface CDCEvent<TPayload = unknown> {
  seq: number;
  projectId: string;
  sessionId?: string;
  type: string;
  payload: TPayload;
  createdAt: string;
}

export interface NotificationCreatedPayload {
  seq: number;
  id: string;
  type: string;
  priority: NotificationPriority;
  message: string;
  data?: Record<string, unknown>;
  actions?: NotificationAction[];
  readAt?: string | null;
  archivedAt?: string | null;
}

export interface NotificationUpdatedPayload {
  seq: number;
  id: string;
  readAt?: string | null;
  archivedAt?: string | null;
}

export interface DesktopNotificationPayload {
  title: string;
  body: string;
  silent: boolean;
  actions: NotificationAction[];
  route?: string;
}

const trustedExternalHosts = new Set(["github.com", "gitlab.com", "linear.app"]);
const desktopActionAllowlist = new Set<NotificationActionKind>([
  "open-session",
  "open-pr",
  "restore-session",
  "mark-read",
]);

export function notificationRecordFromCDCEvent(
  event: CDCEvent<NotificationCreatedPayload>,
): NotificationRecord {
  return {
    seq: event.payload.seq,
    id: event.payload.id,
    receivedAt: event.createdAt,
    readAt: event.payload.readAt ?? null,
    archivedAt: event.payload.archivedAt ?? null,
    event: {
      id: event.payload.id,
      type: event.payload.type,
      priority: event.payload.priority,
      sessionId: event.sessionId ?? "",
      projectId: event.projectId,
      timestamp: event.createdAt,
      message: event.payload.message,
      data: event.payload.data ?? {},
    },
    actions: sanitizeActions(event.payload.actions ?? []),
  };
}

export function sanitizeActions(actions: NotificationAction[]): NotificationAction[] {
  return actions.filter((action) => {
    if (!desktopActionAllowlist.has(action.kind) && action.kind !== "open-review" && action.kind !== "open-ci" && action.kind !== "merge-pr" && action.kind !== "send-message" && action.kind !== "dismiss") {
      return false;
    }
    if (action.route !== undefined && !isSafeInternalRoute(action.route)) {
      return false;
    }
    if (action.url !== undefined && !isSafeExternalUrl(action.url)) {
      return false;
    }
    return true;
  });
}

export function isSafeInternalRoute(route: string): boolean {
  if (!route.startsWith("/") || route.startsWith("//") || route.includes("\\")) {
    return false;
  }
  try {
    const parsed = new URL(route, "http://ao.local");
    return parsed.origin === "http://ao.local" && !route.includes("\n") && !route.includes("\r");
  } catch {
    return false;
  }
}

export function isSafeExternalUrl(raw: string): boolean {
  try {
    const url = new URL(raw);
    if (url.protocol !== "https:") {
      return false;
    }
    const host = url.hostname.toLowerCase();
    for (const allowed of trustedExternalHosts) {
      if (host === allowed || host.endsWith(`.${allowed}`)) {
        return true;
      }
    }
    return false;
  } catch {
    return false;
  }
}

export function shouldShowDesktop(record: NotificationRecord): boolean {
  if (record.archivedAt !== null || record.readAt !== null) {
    return false;
  }
  return record.event.priority === "urgent" || record.event.priority === "action";
}

export function desktopPayloadFor(record: NotificationRecord): DesktopNotificationPayload {
  const lowRiskActions = record.actions.filter((action) => desktopActionAllowlist.has(action.kind));
  if (!lowRiskActions.some((action) => action.kind === "mark-read")) {
    lowRiskActions.push({ id: "mark-read", kind: "mark-read", label: "Mark read" });
  }
  const openSession = record.actions.find((action) => action.kind === "open-session" && action.route !== undefined);
  return {
    title: titleFor(record),
    body: record.event.message,
    silent: record.event.priority !== "urgent",
    actions: lowRiskActions,
    route: openSession?.route,
  };
}

export function titleFor(record: NotificationRecord): string {
  switch (record.event.priority) {
    case "urgent":
      return "AO needs attention";
    case "action":
      return "AO action available";
    case "warning":
      return "AO warning";
    case "info":
      return "AO update";
  }
}

export function reconcileNotification(
  records: NotificationRecord[],
  incoming: NotificationRecord,
): NotificationRecord[] {
  const idx = records.findIndex((record) => record.id === incoming.id);
  if (idx === -1) {
    return [incoming, ...records];
  }
  const next = records.slice();
  next[idx] = incoming;
  return next;
}

export function patchNotificationRecord(
  records: NotificationRecord[],
  patch: NotificationUpdatedPayload,
): NotificationRecord[] {
  return records.map((record) =>
    record.id === patch.id
      ? {
          ...record,
          readAt: patch.readAt === undefined ? record.readAt : patch.readAt,
          archivedAt: patch.archivedAt === undefined ? record.archivedAt : patch.archivedAt,
        }
      : record,
  );
}

export function unreadCount(records: NotificationRecord[]): number {
  return records.filter((record) => record.readAt === null && record.archivedAt === null).length;
}

export function notificationRoute(record: NotificationRecord): string | undefined {
  const action = record.actions.find((candidate) => candidate.kind === "open-session" && candidate.route !== undefined);
  return action?.route;
}

export async function fetchNotifications(
  origin: string,
  query: Record<string, string | number | boolean | undefined> = {},
  fetchImpl: typeof fetch = fetch,
): Promise<NotificationListResponse> {
  const url = new URL("/api/v1/notifications", origin);
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined) {
      url.searchParams.set(key, String(value));
    }
  }
  const response = await fetchImpl(url);
  if (!response.ok) {
    throw new Error(`GET notifications failed with ${response.status}`);
  }
  return (await response.json()) as NotificationListResponse;
}

export function notificationEventsURL(
  origin: string,
  options: { after?: number; projectId?: string; topics?: string[] } = {},
): string {
  const url = new URL("/events", origin);
  if (options.after !== undefined) {
    url.searchParams.set("after", String(options.after));
  }
  if (options.projectId !== undefined) {
    url.searchParams.set("projectId", options.projectId);
  }
  url.searchParams.set("topics", (options.topics ?? ["notifications"]).join(","));
  return url.toString();
}

export function openNotificationEventSource(
  origin: string,
  options: { after?: number; projectId?: string } = {},
): EventSource {
  return new EventSource(notificationEventsURL(origin, { ...options, topics: ["notifications"] }));
}

export async function patchNotification(
  origin: string,
  id: string,
  body: { read?: boolean; archived?: boolean },
  fetchImpl: typeof fetch = fetch,
): Promise<NotificationRecord> {
  const response = await fetchImpl(new URL(`/api/v1/notifications/${encodeURIComponent(id)}`, origin), {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    throw new Error(`PATCH notification failed with ${response.status}`);
  }
  const payload = (await response.json()) as { notification: NotificationRecord };
  return payload.notification;
}

export async function readAllNotifications(
  origin: string,
  body: { projectId?: string; sessionId?: string } = {},
  fetchImpl: typeof fetch = fetch,
): Promise<number> {
  const response = await fetchImpl(new URL("/api/v1/notifications/read-all", origin), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    throw new Error(`read-all failed with ${response.status}`);
  }
  const payload = (await response.json()) as { updated: number };
  return payload.updated;
}

export async function invokeNotificationAction(
  origin: string,
  notificationId: string,
  actionId: string,
  token: string,
  fetchImpl: typeof fetch = fetch,
): Promise<unknown> {
  const response = await fetchImpl(
    new URL(`/api/v1/notifications/${encodeURIComponent(notificationId)}/actions/${encodeURIComponent(actionId)}`, origin),
    {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-AO-Action-Token": token },
      body: "{}",
    },
  );
  if (!response.ok) {
    throw new Error(`notification action failed with ${response.status}`);
  }
  return response.json();
}
