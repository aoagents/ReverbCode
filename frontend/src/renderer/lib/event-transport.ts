import type { QueryClient } from "@tanstack/react-query";
import { aoBridge } from "./bridge";

export type EventTransport = {
  connect: () => () => void;
};

export function createEventTransport(queryClient: QueryClient): EventTransport {
  return {
    connect() {
      const removeDaemonListener = aoBridge.daemon.onStatus(() => {
        void queryClient.invalidateQueries({ queryKey: ["daemon"] });
      });

      return () => {
        removeDaemonListener();
      };
    },
  };
}
