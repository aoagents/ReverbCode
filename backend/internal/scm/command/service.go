package command

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AuditSink records command attempts. It can be backed by the future durable
// audit log; the command service never treats audit success as lifecycle truth.
type AuditSink interface {
	RecordSCMCommand(ctx context.Context, result ports.SCMCommandResult, err error) error
}

// Refresher is satisfied by observer.Observer and intentionally mirrors the
// small part commands need: invalidate caches and schedule a refresh.
type Refresher interface {
	Invalidate(ctx context.Context, subject domain.SCMSubject, reason string) error
}

type cacheInvalidationProvider interface {
	CacheInvalidationPrefixes(subject domain.SCMSubject, cmd ports.SCMCommand) []domain.SCMProviderCachePrefix
}

type Service struct {
	Store     ports.SCMStore
	Providers map[domain.SCMProvider]ports.SCMCommandProvider
	Audit     AuditSink
	Refresh   Refresher
	Clock     func() time.Time
}

func New(store ports.SCMStore, refresh Refresher, providers ...ports.SCMCommandProvider) *Service {
	s := &Service{Store: store, Refresh: refresh, Providers: map[domain.SCMProvider]ports.SCMCommandProvider{}, Clock: time.Now}
	for _, p := range providers {
		if p != nil {
			s.Providers[p.Provider()] = p
		}
	}
	return s
}

func (s *Service) MergeChangeRequest(ctx context.Context, sessionID domain.SessionID, opts ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	return s.run(ctx, sessionID, ports.SCMCommandMerge, opts)
}

func (s *Service) CloseChangeRequest(ctx context.Context, sessionID domain.SessionID, opts ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	return s.run(ctx, sessionID, ports.SCMCommandClose, opts)
}

func (s *Service) CommentOnChangeRequest(ctx context.Context, sessionID domain.SessionID, body string) (ports.SCMCommandResult, error) {
	return s.run(ctx, sessionID, ports.SCMCommandComment, ports.SCMCommandRequest{Body: body})
}

func (s *Service) AssignChangeRequest(ctx context.Context, sessionID domain.SessionID, assignees []string) (ports.SCMCommandResult, error) {
	return s.run(ctx, sessionID, ports.SCMCommandAssign, ports.SCMCommandRequest{Assignees: assignees})
}

func (s *Service) CheckoutChangeRequest(ctx context.Context, sessionID domain.SessionID, workspacePath string) (ports.SCMCommandResult, error) {
	return s.run(ctx, sessionID, ports.SCMCommandCheckout, ports.SCMCommandRequest{WorkspacePath: workspacePath})
}

func (s *Service) run(ctx context.Context, sessionID domain.SessionID, cmd ports.SCMCommand, req ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	if s.Store == nil {
		return ports.SCMCommandResult{}, fmt.Errorf("scm command: nil store")
	}
	if s.Clock == nil {
		s.Clock = time.Now
	}
	subj, ok, err := s.Store.GetSubject(ctx, sessionID)
	if err != nil {
		return ports.SCMCommandResult{}, err
	}
	if !ok {
		return ports.SCMCommandResult{}, fmt.Errorf("scm command: subject %s not found", sessionID)
	}
	provider := s.Providers[subj.Provider]
	if provider == nil {
		return ports.SCMCommandResult{}, &domain.SCMError{Kind: domain.SCMErrorUnsupported, Operation: string(cmd), Message: fmt.Sprintf("provider %q not registered", subj.Provider)}
	}
	req.Subject = subj
	req.ChangeRequest = subj.ChangeRequestID()
	req.Command = cmd
	req.Now = s.Clock()
	var res ports.SCMCommandResult
	switch cmd {
	case ports.SCMCommandMerge:
		if !provider.Capabilities().Merge {
			return res, capabilityError(cmd)
		}
		res, err = provider.Merge(ctx, req)
	case ports.SCMCommandClose:
		if !provider.Capabilities().Close {
			return res, capabilityError(cmd)
		}
		res, err = provider.Close(ctx, req)
	case ports.SCMCommandComment:
		if !provider.Capabilities().Comment {
			return res, capabilityError(cmd)
		}
		res, err = provider.Comment(ctx, req)
	case ports.SCMCommandAssign:
		if !provider.Capabilities().Assign {
			return res, capabilityError(cmd)
		}
		res, err = provider.Assign(ctx, req)
	case ports.SCMCommandCheckout:
		if !provider.Capabilities().Checkout {
			return res, capabilityError(cmd)
		}
		res, err = provider.Checkout(ctx, req)
	default:
		err = capabilityError(cmd)
	}
	if s.Audit != nil {
		_ = s.Audit.RecordSCMCommand(ctx, res, err)
	}
	if err != nil {
		return res, err
	}
	if cmd == ports.SCMCommandCheckout {
		return res, nil
	}
	if err := s.invalidateAfterCommand(ctx, provider, subj, cmd); err != nil {
		return res, err
	}
	if s.Refresh != nil {
		if err := s.Refresh.Invalidate(ctx, subj, string(cmd)); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (s *Service) invalidateAfterCommand(ctx context.Context, provider ports.SCMCommandProvider, subj domain.SCMSubject, cmd ports.SCMCommand) error {
	invalidator, ok := provider.(cacheInvalidationProvider)
	if !ok {
		return nil
	}
	prefixes := invalidator.CacheInvalidationPrefixes(subj, cmd)
	for _, p := range prefixes {
		if err := s.Store.DeleteProviderCache(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func capabilityError(cmd ports.SCMCommand) error {
	return &domain.SCMError{Kind: domain.SCMErrorUnsupported, Operation: string(cmd), Message: "command unsupported by provider"}
}
