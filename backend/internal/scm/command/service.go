package command

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	scmlog "github.com/aoagents/agent-orchestrator/backend/internal/scm/logging"
)

// AuditSink records command attempts. It can be backed by the future durable
// audit log; the command service never treats audit success as lifecycle truth.
type AuditSink interface {
	RecordSCMCommand(ctx context.Context, result ports.SCMCommandResult, err error) error
}

// Refresher is satisfied by observer.Observer and intentionally mirrors the
// small part commands need: schedule a refresh after provider-specific cache
// invalidation has already been applied.
type Refresher interface {
	Refresh(ctx context.Context, subjects []domain.SCMSubject) error
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
	Logger    *slog.Logger
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
	ctx, _ = scmlog.EnsureCorrelationID(ctx)
	logger := scmlog.Logger(s.Logger)
	started := time.Now()
	if s.Store == nil {
		err := fmt.Errorf("scm command: nil store")
		logCommandFailed(ctx, logger, ports.SCMCommandRequest{Command: cmd}, scmlog.DurationMS(started), err)
		return ports.SCMCommandResult{}, err
	}
	if s.Clock == nil {
		s.Clock = time.Now
	}
	subj, ok, err := s.Store.GetSubject(ctx, sessionID)
	if err != nil {
		logCommandFailed(ctx, logger, ports.SCMCommandRequest{Command: cmd, Subject: domain.SCMSubject{SessionID: sessionID}}, scmlog.DurationMS(started), err)
		return ports.SCMCommandResult{}, err
	}
	if !ok {
		err := fmt.Errorf("scm command: subject %s not found", sessionID)
		logCommandFailed(ctx, logger, ports.SCMCommandRequest{Command: cmd, Subject: domain.SCMSubject{SessionID: sessionID}}, scmlog.DurationMS(started), err)
		return ports.SCMCommandResult{}, err
	}
	req.Subject = subj
	req.ChangeRequest = subj.ChangeRequestID()
	req.Command = cmd
	req.Now = s.Clock()
	if subj.PRNumber == 0 {
		err := &domain.SCMError{Kind: domain.SCMErrorNotFound, Operation: string(cmd), Message: "no change request bound to session"}
		res := commandResultForError(req, err)
		logCommandFailed(ctx, logger, req, scmlog.DurationMS(started), err)
		return res, err
	}
	provider := s.Providers[subj.Provider]
	if provider == nil {
		err := &domain.SCMError{Kind: domain.SCMErrorUnsupported, Operation: string(cmd), Message: fmt.Sprintf("provider %q not registered", subj.Provider)}
		res := commandResultForError(req, err)
		logCommandFailed(ctx, logger, req, scmlog.DurationMS(started), err)
		return res, err
	}
	logger.Info(scmlog.EventCommandStarted, scmlog.Args(scmlog.Add(scmlog.CommandAttrs(req), scmlog.CorrelationAttr(ctx)))...)
	var res ports.SCMCommandResult
	switch cmd {
	case ports.SCMCommandMerge:
		if !provider.Capabilities().Merge {
			err = capabilityError(cmd)
			res = commandResultForError(req, err)
			break
		}
		res, err = provider.Merge(ctx, req)
	case ports.SCMCommandClose:
		if !provider.Capabilities().Close {
			err = capabilityError(cmd)
			res = commandResultForError(req, err)
			break
		}
		res, err = provider.Close(ctx, req)
	case ports.SCMCommandComment:
		if !provider.Capabilities().Comment {
			err = capabilityError(cmd)
			res = commandResultForError(req, err)
			break
		}
		res, err = provider.Comment(ctx, req)
	case ports.SCMCommandAssign:
		if !provider.Capabilities().Assign {
			err = capabilityError(cmd)
			res = commandResultForError(req, err)
			break
		}
		res, err = provider.Assign(ctx, req)
	case ports.SCMCommandCheckout:
		if !provider.Capabilities().Checkout {
			err = capabilityError(cmd)
			res = commandResultForError(req, err)
			break
		}
		res, err = provider.Checkout(ctx, req)
	default:
		err = capabilityError(cmd)
		res = commandResultForError(req, err)
	}
	res = normalizeResult(res, req)
	if err != nil {
		res = attachErrorDiagnostic(res, req, err)
	}
	if s.Audit != nil {
		if auditErr := s.Audit.RecordSCMCommand(ctx, res, err); auditErr != nil {
			attrs := scmlog.Add(scmlog.CommandAttrs(req), scmlog.CorrelationAttr(ctx))
			attrs = append(attrs, scmlog.ErrorAttrs(auditErr)...)
			logger.Warn(scmlog.EventCommandAuditFailed, scmlog.Args(attrs)...)
		}
	}
	if err != nil {
		logCommandFailed(ctx, logger, req, scmlog.DurationMS(started), err)
		return res, err
	}
	logCommandCompleted(ctx, logger, req, scmlog.DurationMS(started))
	if cmd == ports.SCMCommandCheckout {
		return res, nil
	}
	if invalidated, err := s.invalidateAfterCommand(ctx, provider, subj, cmd); err != nil {
		logCommandFailed(ctx, logger, req, scmlog.DurationMS(started), err)
		return res, err
	} else if invalidated > 0 {
		attrs := scmlog.Add(scmlog.CommandAttrs(req),
			scmlog.CorrelationAttr(ctx),
			slog.Int("cache_prefix_count", invalidated),
		)
		logger.Debug(scmlog.EventCommandCacheInvalid, scmlog.Args(attrs)...)
	}
	if s.Refresh != nil {
		if err := s.Refresh.Refresh(ctx, []domain.SCMSubject{subj}); err != nil {
			attrs := scmlog.Add(scmlog.CommandAttrs(req),
				scmlog.CorrelationAttr(ctx),
				slog.Int64(scmlog.FieldDurationMS, scmlog.DurationMS(started)),
			)
			attrs = append(attrs, scmlog.ErrorAttrs(err)...)
			logger.Warn(scmlog.EventCommandRefreshFailed, scmlog.Args(attrs)...)
			return res, err
		}
	}
	return res, nil
}

func (s *Service) invalidateAfterCommand(ctx context.Context, provider ports.SCMCommandProvider, subj domain.SCMSubject, cmd ports.SCMCommand) (int, error) {
	invalidator, ok := provider.(cacheInvalidationProvider)
	if !ok {
		return 0, nil
	}
	prefixes := invalidator.CacheInvalidationPrefixes(subj, cmd)
	for _, p := range prefixes {
		if err := s.Store.DeleteProviderCache(ctx, p); err != nil {
			return 0, err
		}
	}
	return len(prefixes), nil
}

func capabilityError(cmd ports.SCMCommand) error {
	return &domain.SCMError{Kind: domain.SCMErrorUnsupported, Operation: string(cmd), Message: "command unsupported by provider"}
}

func normalizeResult(res ports.SCMCommandResult, req ports.SCMCommandRequest) ports.SCMCommandResult {
	if res.Provider == "" {
		res.Provider = req.Subject.Provider
	}
	if res.Command == "" {
		res.Command = req.Command
	}
	if res.ChangeRequest.Number == 0 {
		res.ChangeRequest = req.ChangeRequest
	}
	if res.PerformedAt.IsZero() {
		res.PerformedAt = req.Now
	}
	return res
}

func commandResultForError(req ports.SCMCommandRequest, err error) ports.SCMCommandResult {
	return attachErrorDiagnostic(normalizeResult(ports.SCMCommandResult{}, req), req, err)
}

func attachErrorDiagnostic(res ports.SCMCommandResult, req ports.SCMCommandRequest, err error) ports.SCMCommandResult {
	if len(res.Diagnostics) == 0 {
		res.Diagnostics = append(res.Diagnostics, scmlog.DiagnosticFromError(string(req.Command), err))
		return res
	}
	for i := range res.Diagnostics {
		if res.Diagnostics[i].ErrorKind == "" {
			res.Diagnostics[i].ErrorKind = scmlog.ErrorKind(err)
		}
		if res.Diagnostics[i].StatusCode == 0 {
			if se, ok := scmlog.SCMError(err); ok {
				res.Diagnostics[i].StatusCode = se.StatusCode
			}
		}
		if res.Diagnostics[i].Message == "" {
			res.Diagnostics[i].Message = scmlog.SafeDiagnosticMessage(err)
		}
	}
	return res
}

func logCommandCompleted(ctx context.Context, logger *slog.Logger, req ports.SCMCommandRequest, durationMS int64) {
	attrs := scmlog.Add(scmlog.CommandAttrs(req),
		scmlog.CorrelationAttr(ctx),
		slog.Int64(scmlog.FieldDurationMS, durationMS),
	)
	logger.Info(scmlog.EventCommandCompleted, scmlog.Args(attrs)...)
}

func logCommandFailed(ctx context.Context, logger *slog.Logger, req ports.SCMCommandRequest, durationMS int64, err error) {
	attrs := scmlog.Add(scmlog.CommandAttrs(req),
		scmlog.CorrelationAttr(ctx),
		slog.Int64(scmlog.FieldDurationMS, durationMS),
	)
	attrs = append(attrs, scmlog.ErrorAttrs(err)...)
	if _, ok := scmlog.SCMError(err); ok {
		logger.Warn(scmlog.EventCommandFailed, scmlog.Args(attrs)...)
		return
	}
	logger.Error(scmlog.EventCommandFailed, scmlog.Args(attrs)...)
}
