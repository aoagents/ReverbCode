// spawn.go - injectable hostSpawner seam. The real detached-process spawn is
// Windows-only (spawn_windows.go). This file defines the type and the
// defaultSpawnHost variable; the non-windows stub is in spawn_other.go.
package conpty

import "context"

// hostSpawner starts a detached pty-host for the session and returns its
// loopback address ("127.0.0.1:PORT") and OS pid once it prints READY.
// Injectable for tests: replace this field on Options before calling New.
type hostSpawner func(ctx context.Context, sessionID, cwd string, argv []string, env map[string]string) (addr string, pid int, err error)
