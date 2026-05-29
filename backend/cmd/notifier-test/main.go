// notifier-test fires one macOS desktop notification via the desktop
// Notifier adapter. Intended for local smoke-testing only.
//
// Usage: notifier-test "title" "body"
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/notifier/desktop"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <title> <body>\n", os.Args[0])
		os.Exit(1)
	}
	n, err := desktop.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		os.Exit(1)
	}
	if err := n.Notify(context.Background(), ports.OrchestratorEvent{
		// The desktop adapter maps Type→title, Message→body. We reuse those
		// fields here purely as a smoke-test plumbing convenience; real callers
		// (LCM reactions) populate Type with the canonical event kind.
		Type:    os.Args[1],
		Message: os.Args[2],
	}); err != nil {
		fmt.Fprintf(os.Stderr, "notify: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("OK")
}
