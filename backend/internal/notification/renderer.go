package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Reader is the subset of durable state the renderer rehydrates. *sqlite.Store
// satisfies it directly.
type Reader interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	PRFactsForSession(ctx context.Context, id domain.SessionID) (domain.PRFacts, error)
}

// Renderer converts lifecycle notification events into durable notification rows.
type Renderer struct {
	reader Reader
	clock  func() time.Time
}

// NewRenderer returns a Renderer that sources session/PR facts via reader.
func NewRenderer(reader Reader) *Renderer {
	return &Renderer{reader: reader, clock: time.Now}
}

// Render builds a durable Notification (subject + typed payload) from a
// lifecycle Event.
func (r *Renderer) Render(ctx context.Context, event ports.Event) (domain.Notification, error) {
	if event.SessionID == "" {
		return domain.Notification{}, fmt.Errorf("render notification: missing session id")
	}
	rec, ok, err := r.reader.GetSession(ctx, event.SessionID)
	if err != nil {
		return domain.Notification{}, fmt.Errorf("render notification: get session %s: %w", event.SessionID, err)
	}
	if !ok {
		return domain.Notification{}, fmt.Errorf("render notification: session %s not found", event.SessionID)
	}
	pr, err := r.reader.PRFactsForSession(ctx, event.SessionID)
	if err != nil {
		return domain.Notification{}, fmt.Errorf("render notification: pr facts for %s: %w", event.SessionID, err)
	}

	projectID := event.ProjectID
	if projectID == "" {
		projectID = rec.ProjectID
	}
	reaction := reactionPayload(event)
	semanticType := SemanticTypeForReaction(reaction.Key)
	if semanticType == "" {
		semanticType = event.Type
	}
	payload := Payload{
		SchemaVersion: PayloadSchemaVersion,
		SemanticType:  semanticType,
		Subject: SubjectPayload{
			Session: &SessionSubjectPayload{ID: string(event.SessionID), ProjectID: string(projectID)},
			Branch:  rec.Metadata.Branch,
		},
		Reaction: &reaction,
	}
	if rec.IssueID != "" {
		payload.Subject.Issue = &IssueSubjectPayload{ID: string(rec.IssueID)}
	}
	if pr.Exists {
		payload.Subject.PR = &PRSubjectPayload{Number: pr.Number, URL: pr.URL, Draft: pr.Draft}
		if pr.CI != "" {
			payload.CI = &CIPayload{Status: string(pr.CI)}
		}
		if pr.Review != "" {
			payload.Review = &ReviewPayload{Decision: string(pr.Review)}
		}
		payload.Merge = mergePayload(pr.Mergeability)
	}
	if event.Escalation != nil {
		payload.Escalation = &EscalationPayload{
			Attempts:   event.Escalation.Attempts,
			Cause:      event.Escalation.Cause,
			DurationMs: event.Escalation.DurationMs,
		}
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return domain.Notification{}, fmt.Errorf("render notification payload: %w", err)
	}

	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = r.clock().UTC()
	}
	priority := string(event.Priority)
	if priority == "" {
		priority = string(ports.PriorityInfo)
	}
	dedupeKey := event.DedupeKey
	if dedupeKey == "" {
		dedupeKey = ComputeDedupeKey(event, rec, pr)
	}
	causeKey := event.CauseKey
	if causeKey == "" {
		causeKey = reaction.Key
		if event.Escalation != nil && event.Escalation.Cause != "" {
			causeKey += ":" + event.Escalation.Cause
		}
	}

	return domain.Notification{
		ProjectID:    projectID,
		SessionID:    event.SessionID,
		Source:       domain.NotificationSourceLifecycle,
		EventType:    domain.NotificationEventType(event.Type),
		SemanticType: domain.NotificationSemanticType(semanticType),
		Priority:     domain.NotificationPriority(priority),
		Message:      event.Message,
		Payload:      payloadJSON,
		Actions:      actionsFor(projectID, event.SessionID, pr),
		DedupeKey:    dedupeKey,
		CauseKey:     causeKey,
		CreatedAt:    occurredAt,
		UpdatedAt:    occurredAt,
	}, nil
}

func reactionPayload(event ports.Event) ReactionPayload {
	key := reactionKeyFromType(event.Type)
	action := "notify"
	if event.Reaction != nil {
		if event.Reaction.Key != "" {
			key = event.Reaction.Key
		}
		if event.Reaction.Action != "" {
			action = event.Reaction.Action
		}
	}
	if event.Escalation != nil && event.Reaction == nil {
		action = "escalated"
	}
	return ReactionPayload{Key: key, Action: action}
}

func reactionKeyFromType(t string) string {
	if strings.HasPrefix(t, "reaction.") {
		return strings.TrimPrefix(t, "reaction.")
	}
	return t
}

func mergePayload(m domain.Mergeability) *MergePayload {
	if m == "" {
		return nil
	}
	ready := m == domain.MergeMergeable
	conflicts := m == domain.MergeConflicting
	return &MergePayload{Ready: &ready, Conflicts: &conflicts}
}

func actionsFor(projectID domain.ProjectID, sessionID domain.SessionID, pr domain.PRFacts) []domain.NotificationAction {
	actions := []domain.NotificationAction{{
		ID:    "open-session",
		Kind:  "route",
		Label: "Open session",
		Route: fmt.Sprintf("/projects/%s/sessions/%s", projectID, sessionID),
	}}
	if pr.Exists && pr.URL != "" {
		actions = append(actions, domain.NotificationAction{ID: "open-pr", Kind: "url", Label: "Open PR", URL: pr.URL})
	}
	return actions
}

// SemanticTypeForReaction maps internal reaction keys to public semantic types.
func SemanticTypeForReaction(key string) string {
	switch key {
	case "approved-and-green":
		return "merge.ready"
	case "agent-stuck":
		return "session.stuck"
	case "agent-needs-input":
		return "session.needs_input"
	case "pr-closed":
		return "pr.closed"
	case "pr-merged":
		return "pr.merged"
	case "ci-failed":
		return "ci.failing"
	case "review-comments":
		return "review.changes_requested"
	case "merge-conflicts":
		return "merge.conflicts"
	default:
		return ""
	}
}
