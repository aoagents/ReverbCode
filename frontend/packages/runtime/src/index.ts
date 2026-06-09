// @aoagents/runtime — React context/hook layer for the AO web rewrite.
// Depends on @aoagents/core. The MuxProvider, terminal-mux hooks, and the
// status shim land in later phases; this entry stub establishes the
// runtime -> core dependency edge so the workspace graph resolves now.
import { CORE_PACKAGE } from "@aoagents/core";

export const RUNTIME_PACKAGE = "@aoagents/runtime" as const;

/** Re-exported here only to exercise the runtime -> core workspace edge. */
export const RUNTIME_CORE_DEP = CORE_PACKAGE;
