import assert from "node:assert/strict";
import test from "node:test";

import {
  initialNotificationState,
  notificationReducer,
  visibleNotifications,
} from "./notification-state";
import { CDCEvent, NotificationCreatedPayload, NotificationRecord } from "./notifications";

test("notification reducer handles snapshot, unread badge, and filters", () => {
  const records = [record("n1", "urgent", null), record("n2", "info", "2026-05-31T10:31:00Z")];
  const state = notificationReducer(initialNotificationState(), {
    type: "snapshot",
    snapshot: { notifications: records, unreadCount: 1, limit: 50, nextBeforeSeq: null },
  });
  assert.equal(state.status, "ready");
  assert.equal(state.unreadCount, 1);

  const unread = notificationReducer(state, { type: "set-filter", filter: "unread" });
  assert.deepEqual(
    visibleNotifications(unread).map((item) => item.id),
    ["n1"],
  );
});

test("notification reducer appends created and patches updated SSE events", () => {
  let state = notificationReducer(initialNotificationState(), {
    type: "snapshot",
    snapshot: { notifications: [], unreadCount: 0, limit: 50, nextBeforeSeq: null },
  });
  state = notificationReducer(state, { type: "sse", event: createdEvent("n3") });
  assert.equal(state.records[0]?.id, "n3");
  assert.equal(state.unreadCount, 1);

  state = notificationReducer(state, {
    type: "sse",
    event: {
      seq: 11,
      projectId: "ao",
      sessionId: "ao-1",
      type: "notification_updated",
      createdAt: "2026-05-31T10:32:00Z",
      payload: { seq: 3, id: "n3", readAt: "2026-05-31T10:32:00Z" },
    },
  });
  assert.equal(state.records[0]?.readAt, "2026-05-31T10:32:00Z");
  assert.equal(state.unreadCount, 0);
});

test("notification reducer keeps stale records visible while reconnecting or erroring", () => {
  let state = notificationReducer(initialNotificationState(), {
    type: "snapshot",
    snapshot: { notifications: [record("n1", "urgent", null)], unreadCount: 1, limit: 50, nextBeforeSeq: null },
  });
  state = notificationReducer(state, { type: "reconnecting" });
  assert.equal(state.status, "reconnecting");
  assert.equal(visibleNotifications(state).length, 1);

  state = notificationReducer(state, { type: "error", message: "boom" });
  assert.equal(state.status, "error");
  assert.equal(visibleNotifications(state).length, 1);
});

function record(id: string, priority: NotificationRecord["event"]["priority"], readAt: string | null): NotificationRecord {
  return {
    seq: Number(id.replace("n", "")),
    id,
    receivedAt: "2026-05-31T10:30:00Z",
    readAt,
    archivedAt: null,
    event: {
      id,
      type: "session.needs_input",
      priority,
      sessionId: "ao-1",
      projectId: "ao",
      timestamp: "2026-05-31T10:30:00Z",
      message: "Agent needs input.",
      data: {},
    },
    actions: [{ id: "open-session", kind: "open-session", label: "Open session", route: "/projects/ao/sessions/ao-1" }],
  };
}

function createdEvent(id: string): CDCEvent<NotificationCreatedPayload> {
  return {
    seq: 10,
    projectId: "ao",
    sessionId: "ao-1",
    type: "notification_created",
    createdAt: "2026-05-31T10:31:00Z",
    payload: {
      seq: 3,
      id,
      type: "session.needs_input",
      priority: "urgent",
      message: "Agent needs input.",
      data: {},
      actions: [{ id: "open-session", kind: "open-session", label: "Open session", route: "/projects/ao/sessions/ao-1" }],
      readAt: null,
      archivedAt: null,
    },
  };
}
