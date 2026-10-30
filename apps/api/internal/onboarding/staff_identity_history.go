package onboarding

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

type StaffIdentityHistoryReceipt struct {
	SchemaVersion   int                              `json:"schema_version"`
	CommandID       uuid.UUID                        `json:"command_id"`
	TenantID        uuid.UUID                        `json:"tenant_id"`
	ProductKey      string                           `json:"product_key"`
	ActorSubject    string                           `json:"actor_subject"`
	Items           []store.StaffIdentityHistoryItem `json:"items"`
	HasMore         bool                             `json:"has_more"`
	NextCursor      string                           `json:"next_cursor,omitempty"`
	CursorExpiresAt *time.Time                       `json:"cursor_expires_at,omitempty"`
}

func (s *Service) StaffIdentityHistory(ctx context.Context, p authz.Principal, tenant, command uuid.UUID, actor, cursor string, limit int) (StaffIdentityHistoryReceipt, error) {
	empty := StaffIdentityHistoryReceipt{}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	scope, err := s.staffIdentityScope(ctx, p, tenant, command, actor, authz.ActionStaffIdentityHistory)
	if err != nil {
		return empty, err
	}
	if limit < 1 || limit > 100 {
		return empty, ErrInvalid
	}
	beforeHash := ""
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != cursor {
			return empty, ErrInvalid
		}
		digest := sha256.Sum256([]byte(cursor))
		beforeHash = hex.EncodeToString(digest[:])
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return empty, ErrUnavailable
	}
	next := base64.RawURLEncoding.EncodeToString(raw[:])
	digest := sha256.Sum256([]byte(next))
	page, err := s.store.ReadStaffIdentityHistory(ctx, scope, beforeHash, hex.EncodeToString(digest[:]), limit, staffIdentityEvent(ctx, p, scope, "staff.identity.history", nil))
	if errors.Is(err, store.ErrNotFound) {
		s.auditTarget(ctx, p, "staff.identity.history", tenant.String(), command.String(), audit.OutcomeDenied, nil)
		return empty, ErrNotFound
	}
	if errors.Is(err, store.ErrStaffHistoryCursor) {
		s.auditTarget(ctx, p, "staff.identity.history", tenant.String(), command.String(), audit.OutcomeDenied, nil)
		return empty, ErrInvalid
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	result := StaffIdentityHistoryReceipt{SchemaVersion: 1, CommandID: command, TenantID: tenant, ProductKey: p.ProductKey, ActorSubject: actor, Items: page.Items, HasMore: page.HasMore}
	if page.HasMore {
		result.NextCursor = next
		result.CursorExpiresAt = &page.CursorExpiresAt
	}
	return result, nil
}
