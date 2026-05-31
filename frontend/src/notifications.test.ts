import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";

import {
  desktopPayloadFor,
  isSafeExternalUrl,
  isSafeInternalRoute,
  notificationEventsURL,
  notificationRecordFromCDCEvent,
  shouldShowDesktop,
  type CDCEvent,
  type NotificationCreatedPayload,
  type NotificationRecord,
} from "./notifications";

test("safe link helpers reject unsafe internal and external targets", () => {
  assert.equal(isSafeInternalRoute("/projects/ao/sessions/ao-1"), true);
  assert.equal(isSafeInternalRoute("javascript:alert(1)"), false);
  assert.equal(isSafeInternalRoute("//evil.example"), false);
  assert.equal(isSafeInternalRoute("/projects\\ao"), false);

  assert.equal(isSafeExternalUrl("https://github.com/aoagents/agent-orchestrator/pull/1"), true);
  assert.equal(isSafeExternalUrl("https://linear.app/ao/issue/AO-1"), true);
  assert.equal(isSafeExternalUrl("javascript:alert(1)"), false);
  assert.equal(isSafeExternalUrl("data:text/plain,hi"), false);
  assert.equal(isSafeExternalUrl("http://github.com/aoagents/agent-orchestrator/pull/1"), false);
  assert.equal(isSafeExternalUrl("https://example.com/pr/1"), false);
});

test("desktop eligibility and payload follow priority defaults", () => {
  const urgent = record("urgent");
  assert.equal(shouldShowDesktop(urgent), true);
  assert.equal(desktopPayloadFor(urgent).silent, false);
  assert.equal(desktopPayloadFor(urgent).route, "/projects/ao/sessions/ao-1");

  const action = { ...urgent, event: { ...urgent.event, priority: "action" as const } };
  assert.equal(shouldShowDesktop(action), true);
  assert.equal(desktopPayloadFor(action).silent, true);

  const warning = { ...urgent, event: { ...urgent.event, priority: "warning" as const } };
  assert.equal(shouldShowDesktop(warning), false);
});

test("CDC notification_created converts to dashboard record and drops unsafe actions", () => {
  const event: CDCEvent<NotificationCreatedPayload> = {
    seq: 100,
    projectId: "ao",
    sessionId: "ao-1",
    type: "notification_created",
    createdAt: "2026-05-31T10:30:00Z",
    payload: {
      seq: 7,
      id: "ntf_7",
      type: "session.needs_input",
      priority: "urgent",
      message: "Agent needs input.",
      data: {},
      actions: [
        { id: "open-session", kind: "open-session", label: "Open", route: "/projects/ao/sessions/ao-1" },
        { id: "open-pr", kind: "open-pr", label: "PR", url: "http://github.com/not/https" },
      ],
    },
  };
  const converted = notificationRecordFromCDCEvent(event);
  assert.equal(converted.id, "ntf_7");
  assert.equal(converted.actions.length, 1);
  assert.equal(converted.actions[0]?.kind, "open-session");
});

test("notificationEventsURL targets notification SSE topic with optional replay cursor", () => {
  const url = notificationEventsURL("http://127.0.0.1:3001", { after: 42, projectId: "ao" });
  assert.equal(url, "http://127.0.0.1:3001/events?after=42&projectId=ao&topics=notifications");
});

test("frontend production code does not call legacy external notification backends", () => {
  const productionFiles = ["main.js", "notifications.js", "notification-state.js"];
  const banned = ["terminal-notifier", "osascript", "notify-send", "powershell.exe", "Slack", "Discord", "webhook"];
  for (const file of productionFiles) {
    const source = readFileSync(join(__dirname, file), "utf8");
    for (const token of banned) {
      assert.equal(source.includes(token), false, `${file} unexpectedly references ${token}`);
    }
  }
});

function record(priority: NotificationRecord["event"]["priority"]): NotificationRecord {
  return {
    seq: 1,
    id: "ntf_1",
    receivedAt: "2026-05-31T10:30:00Z",
    readAt: null,
    archivedAt: null,
    event: {
      id: "ntf_1",
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
