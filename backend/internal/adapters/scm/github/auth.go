package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// TokenSource supplies GitHub bearer tokens. It is intentionally tiny so tests
// can inject a static token and production can use gh auth.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type tokenInvalidator interface {
	InvalidateToken()
}

type StaticTokenSource string

func (s StaticTokenSource) Token(context.Context) (string, error) {
	if strings.TrimSpace(string(s)) == "" {
		return "", ErrNoToken
	}
	return strings.TrimSpace(string(s)), nil
}

var ErrNoToken = errors.New("github scm: no token")

const defaultGHTokenCacheTTL = 5 * time.Minute

// GHTokenSource uses `gh auth token` as the sole default production credential
// source. The token is memoized briefly because every REST/GraphQL request asks
// the client for a token, but it is never cached permanently: the client clears
// it on auth failures and this source refreshes it after TokenTTL.
type GHTokenSource struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
	Command   func(context.Context) ([]byte, error)
	Clock     func() time.Time
	TokenTTL  time.Duration
}

func (s *GHTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && now.Before(s.expiresAt) {
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
	s.expiresAt = now.Add(s.ttl())
	return token, nil
}

func (s *GHTokenSource) InvalidateToken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = ""
	s.expiresAt = time.Time{}
}

func (s *GHTokenSource) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s *GHTokenSource) ttl() time.Duration {
	if s.TokenTTL > 0 {
		return s.TokenTTL
	}
	return defaultGHTokenCacheTTL
}

func credentialHash(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}
