package project

// Test-only handles to unexported internals, so external tests can assert the
// isGitRepo behavior without widening the package's public surface.

// SamePathForTest exposes samePath to tests.
var SamePathForTest = samePath

// NewExecGitCheckerForTest returns the production GitChecker for tests.
func NewExecGitCheckerForTest() GitChecker { return execGitChecker{} }
