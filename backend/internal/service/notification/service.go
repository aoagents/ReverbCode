package notification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Service validates notification intent, enriches it from local durable facts,
// builds semantic actions and canonical copy, and persists a deduped
// notification row.
type Service struct {
	store  Store
	maker  Maker
	clock  func() time.Time
	logger *slog.Logger
}

// Deps are the collaborators needed by Service.
type Deps struct {
	Store  Store
	Maker  Maker
	Clock  func() time.Time
	Logger *slog.Logger
}

// New builds a notification service with sensible defaults.
func New(deps Deps) *Service {
	s := &Service{store: deps.Store, maker: deps.Maker, clock: deps.Clock, logger: deps.Logger}
	if s.maker == nil {
		s.maker = DefaultMaker{}
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	return s
}

// Notify persists or updates the logical notification represented by intent.
// The bool returned by Store.UpsertNotification is deliberately swallowed here:
// callers care that the intent has been durably handled, not whether it inserted,
// updated, or no-op deduped.
func (s *Service) Notify(ctx context.Context, intent domain.NotificationIntent) error {
	if s.store == nil {
		return fmt.Errorf("notification: nil store")
	}
	if intent.OccurredAt.IsZero() {
		intent.OccurredAt = s.clock().UTC()
	}
	if err := intent.Validate(); err != nil {
		return err
	}

	facts, err := s.enrich(ctx, intent)
	if err != nil {
		return err
	}
	actions := buildActions(intent, facts)
	content, err := s.maker.Make(ctx, MakeInput{Intent: intent, Facts: facts, Actions: actions})
	if err != nil {
		return fmt.Errorf("notification: make content: %w", err)
	}
	if content.Title == "" || content.Summary == "" {
		return fmt.Errorf("notification: maker returned empty title or summary")
	}
	fp, err := fingerprint(intent, facts, actions, content)
	if err != nil {
		return fmt.Errorf("notification: fingerprint: %w", err)
	}

	now := s.clock().UTC()
	sessionID := intent.SessionID
	n := domain.Notification{
		ID:          newNotificationID(),
		Type:        intent.Type,
		Priority:    intent.Priority,
		Status:      domain.NotificationUnread,
		ProjectID:   intent.ProjectID,
		SessionID:   &sessionID,
		Source:      intent.Source,
		DedupeKey:   intent.DedupeKey,
		Fingerprint: fp,
		Title:       content.Title,
		Summary:     content.Summary,
		Body:        content.Body,
		Subject:     subjectForFacts(facts),
		Data:        dataForIntent(intent, facts),
		Actions:     actions,
		OccurredAt:  intent.OccurredAt.UTC(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}.Normalize()
	if err := n.Validate(); err != nil {
		return err
	}
	stored, _, err := s.store.UpsertNotification(ctx, n)
	if err != nil {
		return fmt.Errorf("notification: persist %s: %w", intent.DedupeKey, err)
	}
	if intent.Type == domain.NotificationMergeCompleted {
		if err := s.resolveSupersededPRNotifications(ctx, stored, facts, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) resolveSupersededPRNotifications(ctx context.Context, n domain.Notification, facts EnrichedFacts, resolvedAt time.Time) error {
	prURL := facts.PRURL
	if prURL == "" {
		prURL = n.Subject.PRURL
	}
	if prURL == "" {
		return nil
	}
	count, err := s.store.ResolveNotifications(ctx, domain.NotificationResolveFilter{
		ProjectID: n.ProjectID,
		SessionID: n.SessionID,
		PRURL:     prURL,
		Types: []domain.NotificationType{
			domain.NotificationCIFailing,
			domain.NotificationReviewChanges,
			domain.NotificationMergeConflicts,
			domain.NotificationMergeReady,
		},
		Statuses: []domain.NotificationStatus{domain.NotificationUnread, domain.NotificationRead},
	}, resolvedAt)
	if err != nil {
		return fmt.Errorf("notification: resolve superseded PR notifications: %w", err)
	}
	if count > 0 {
		s.logger.Debug("resolved superseded notification rows", "count", count, "pr", prURL)
	}
	return nil
}

func newNotificationID() domain.NotificationID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return domain.NotificationID(fmt.Sprintf("ntf_%d", time.Now().UnixNano()))
	}
	return domain.NotificationID("ntf_" + hex.EncodeToString(b[:]))
}
