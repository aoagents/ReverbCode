import { useSyncExternalStore } from "react";
import {
	getEventsConnectionState,
	subscribeEventsConnection,
	type EventsConnectionState,
} from "../lib/events-connection";

/** Live connection state of the daemon SSE stream (see events-connection.ts). */
export function useEventsConnection(): EventsConnectionState {
	return useSyncExternalStore(subscribeEventsConnection, getEventsConnectionState);
}
