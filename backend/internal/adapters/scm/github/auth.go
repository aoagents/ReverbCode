package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"strings"
	"sync"
)

// TokenSource supplies GitHub bearer tokens. It is intentionally tiny so tests
// can inject a static token and production can use gh auth.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type StaticTokenSource string

func (s StaticTokenSource) Token(context.Context) (string, error) {
	if strings.TrimSpace(string(s)) == "" {
		return "", ErrNoToken
	}
	return strings.TrimSpace(string(s)), nil
}

var ErrNoToken = errors.New("github scm: no token")

// GHTokenSource uses `gh auth token` as the sole default production credential
// source. The token is memoized because every REST/GraphQL request asks the
// client for a token.
type GHTokenSource struct {
	mu      sync.Mutex
	token   string
	Command func(context.Context) ([]byte, error)
}

func (s *GHTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" {
		return s.token, nil
	}
	run := s.Command
	if run == nil {
		run = func(ctx context.Context) ([]byte, error) {
			return exec.CommandContext(ctx, "gh", "auth", "token").Output()
		}
	}
	out, err := run(ctx)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", ErrNoToken
	}
	s.token = token
	return token, nil
}

func credentialHash(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}
