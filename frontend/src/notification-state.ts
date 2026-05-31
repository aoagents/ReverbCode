import {
  CDCEvent,
  NotificationCreatedPayload,
  NotificationListResponse,
  NotificationRecord,
  NotificationUpdatedPayload,
  notificationRecordFromCDCEvent,
  patchNotificationRecord,
  reconcileNotification,
  unreadCount,
} from "./notifications";

export type NotificationStatus = "loading" | "ready" | "empty-all" | "empty-unread" | "error" | "reconnecting";
export type NotificationFilter = "all" | "unread";
export type ActionStatus = "pending" | "success" | "failure";

export interface NotificationState {
  status: NotificationStatus;
  filter: NotificationFilter;
  records: NotificationRecord[];
  unreadCount: number;
  error?: string;
  actionStatus: Record<string, ActionStatus>;
}

export type NotificationStateAction =
  | { type: "snapshot"; snapshot: NotificationListResponse }
  | { type: "sse"; event: CDCEvent<NotificationCreatedPayload | NotificationUpdatedPayload> }
  | { type: "set-filter"; filter: NotificationFilter }
  | { type: "mark-read"; id: string; readAt: string }
  | { type: "mark-all-read"; readAt: string; projectId?: string; sessionId?: string }
  | { type: "dismiss"; id: string; archivedAt: string }
  | { type: "error"; message: string }
  | { type: "reconnecting" }
  | { type: "action-status"; key: string; status: ActionStatus };

export function initialNotificationState(filter: NotificationFilter = "all"): NotificationState {
  return {
    status: "loading",
    filter,
    records: [],
    unreadCount: 0,
    actionStatus: {},
  };
}

export function notificationReducer(state: NotificationState, action: NotificationStateAction): NotificationState {
  switch (action.type) {
    case "snapshot":
      return withDerivedStatus({
        ...state,
        records: action.snapshot.notifications,
        unreadCount: action.snapshot.unreadCount,
        error: undefined,
      });
    case "sse":
      if (action.event.type === "notification_created") {
        const incoming = notificationRecordFromCDCEvent(action.event as CDCEvent<NotificationCreatedPayload>);
        return withDerivedStatus({
          ...state,
          records: reconcileNotification(state.records, incoming),
          unreadCount: unreadCount(reconcileNotification(state.records, incoming)),
        });
      }
      if (action.event.type === "notification_updated") {
        const records = patchNotificationRecord(state.records, action.event.payload as NotificationUpdatedPayload);
        return withDerivedStatus({ ...state, records, unreadCount: unreadCount(records) });
      }
      return state;
    case "set-filter":
      return withDerivedStatus({ ...state, filter: action.filter });
    case "mark-read": {
      const records = state.records.map((record) => (record.id === action.id ? { ...record, readAt: action.readAt } : record));
      return withDerivedStatus({ ...state, records, unreadCount: unreadCount(records) });
    }
    case "mark-all-read": {
      const records = state.records.map((record) => {
        const projectMatches = action.projectId === undefined || record.event.projectId === action.projectId;
        const sessionMatches = action.sessionId === undefined || record.event.sessionId === action.sessionId;
        return projectMatches && sessionMatches && record.archivedAt === null ? { ...record, readAt: action.readAt } : record;
      });
      return withDerivedStatus({ ...state, records, unreadCount: unreadCount(records) });
    }
    case "dismiss": {
      const records = state.records.map((record) => (record.id === action.id ? { ...record, archivedAt: action.archivedAt } : record));
      return withDerivedStatus({ ...state, records, unreadCount: unreadCount(records) });
    }
    case "error":
      return { ...state, status: "error", error: action.message };
    case "reconnecting":
      return { ...state, status: "reconnecting" };
    case "action-status":
      return { ...state, actionStatus: { ...state.actionStatus, [action.key]: action.status } };
  }
}

export function visibleNotifications(state: NotificationState): NotificationRecord[] {
  const active = state.records.filter((record) => record.archivedAt === null);
  if (state.filter === "unread") {
    return active.filter((record) => record.readAt === null);
  }
  return active;
}

export function withDerivedStatus(state: NotificationState): NotificationState {
  const visible = visibleNotifications(state);
  let status: NotificationStatus = "ready";
  if (state.filter === "unread" && visible.length === 0) {
    status = "empty-unread";
  } else if (state.filter === "all" && visible.length === 0) {
    status = "empty-all";
  }
  return { ...state, status };
}
