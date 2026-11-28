package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

var ErrRecoverySessionRejected = errors.New("native recovery authentication required")

type RecoverySessionProof struct {
	Sequence           string
	CreatedAt          time.Time
	PasswordVerifiedAt time.Time
	FactorVerifiedAt   time.Time
}

type recoverySessionFactor struct {
	VerifiedAt time.Time `json:"verifiedAt"`
}

type recoveryNativeSession struct {
	ID             string          `json:"id"`
	Sequence       string          `json:"sequence"`
	CreationDate   time.Time       `json:"creationDate"`
	ChangeDate     time.Time       `json:"changeDate"`
	ExpirationDate json.RawMessage `json:"expirationDate"`
	Factors        struct {
		User *struct {
			ID             string    `json:"id"`
			OrganizationID string    `json:"organizationId"`
			VerifiedAt     time.Time `json:"verifiedAt"`
		} `json:"user"`
		Password *recoverySessionFactor `json:"password"`
		TOTP     *recoverySessionFactor `json:"totp"`
		WebAuthN *recoverySessionFactor `json:"webAuthN"`
	} `json:"factors"`
}

// VerifyRecoverySession accepts only an ID from an already verified ID-token sid.
// Its cutoffs must come from the durable recovery generation and native enrollment,
// and kind is the enrolled factor ("totp" or "security_key") the session must prove.
func (c *Client) VerifyRecoverySession(ctx context.Context, orgID, subject, sessionID, kind string, resetAt, enrolledAt time.Time) (_ RecoverySessionProof, resultErr error) {
	stage := "configuration"
	defer func() {
		if c != nil && c.log != nil && errors.Is(resultErr, ErrIdentityUnavailable) {
			c.log.Warn("Staff MFA session proof unavailable", "stage", stage)
		}
	}()
	var empty RecoverySessionProof
	if !identitySourceID.MatchString(orgID) || !identitySourceID.MatchString(subject) || !identitySourceID.MatchString(sessionID) ||
		resetAt.IsZero() || enrolledAt.IsZero() || enrolledAt.Before(resetAt) || enrolledAt.After(time.Now()) {
		return empty, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stage = "token"
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return empty, ErrIdentityUnavailable
	}
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var raw json.RawMessage
	stage = "read"
	err = c.readIdentity(ctx, &client, token, http.MethodGet, "/v2/sessions/"+sessionID, orgID, nil, &raw)
	if errors.Is(err, ErrIdentityMissing) {
		return empty, ErrRecoverySessionRejected
	}
	if err != nil || ctx.Err() != nil {
		return empty, ErrIdentityUnavailable
	}
	stage = "decode"
	session, err := decodeRecoverySession(raw)
	if err != nil {
		return empty, err
	}
	stage = "proof"
	return verifyRecoverySession(session, orgID, subject, sessionID, kind, resetAt, enrolledAt, time.Now())
}

func decodeRecoverySession(raw []byte) (*recoveryNativeSession, error) {
	root, err := identityObject(raw, "session")
	if err != nil {
		return nil, ErrIdentityUnavailable
	}
	session, err := identityObject(root["session"], "id", "sequence", "creationDate", "changeDate", "expirationDate", "factors")
	if err != nil {
		return nil, ErrIdentityUnavailable
	}
	factors, err := identityObject(session["factors"], "user", "password", "totp", "webAuthN")
	if err != nil {
		return nil, ErrIdentityUnavailable
	}
	if _, err = identityObject(factors["user"], "id", "organizationId", "verifiedAt"); err != nil {
		return nil, ErrIdentityUnavailable
	}
	for _, name := range []string{"password", "totp", "webAuthN"} {
		value := factors[name]
		if len(value) != 0 && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			if _, err = identityObject(value, "verifiedAt"); err != nil {
				return nil, ErrIdentityUnavailable
			}
		}
	}
	var out recoveryNativeSession
	if json.Unmarshal(root["session"], &out) != nil {
		return nil, ErrIdentityUnavailable
	}
	return &out, nil
}

func verifyRecoverySession(s *recoveryNativeSession, orgID, subject, sessionID, kind string, resetAt, enrolledAt, now time.Time) (RecoverySessionProof, error) {
	var empty RecoverySessionProof
	user := s.Factors.User
	if !validIdentityRevision(s.Sequence) || !identitySourceID.MatchString(s.ID) || user == nil ||
		!identitySourceID.MatchString(user.ID) || !identitySourceID.MatchString(user.OrganizationID) ||
		!recoverySessionInstant(s.CreationDate, now) || !recoverySessionInstant(s.ChangeDate, now) ||
		s.ChangeDate.Before(s.CreationDate) || !recoverySessionInstant(user.VerifiedAt, now) {
		return empty, ErrIdentityUnavailable
	}
	if s.ID != sessionID || user.ID != subject || user.OrganizationID != orgID {
		return empty, ErrRecoverySessionRejected
	}
	if len(s.ExpirationDate) != 0 {
		var expires time.Time
		if bytes.Equal(bytes.TrimSpace(s.ExpirationDate), []byte("null")) || json.Unmarshal(s.ExpirationDate, &expires) != nil || expires.IsZero() {
			return empty, ErrIdentityUnavailable
		}
		if !expires.After(now) {
			return empty, ErrRecoverySessionRejected
		}
	}
	// Only the factor just enrolled proves the person, never the other kind.
	proved := map[string]*recoverySessionFactor{"totp": s.Factors.TOTP, "security_key": s.Factors.WebAuthN}[kind]
	if s.Factors.Password == nil || proved == nil {
		return empty, ErrRecoverySessionRejected
	}
	primary, factor := s.Factors.Password.VerifiedAt, proved.VerifiedAt
	if !recoverySessionInstant(primary, now) || !recoverySessionInstant(factor, now) {
		return empty, ErrIdentityUnavailable
	}
	if !s.CreationDate.After(resetAt) || !primary.After(resetAt) || !factor.After(enrolledAt) {
		return empty, ErrRecoverySessionRejected
	}
	return RecoverySessionProof{
		Sequence: s.Sequence, CreatedAt: s.CreationDate,
		PasswordVerifiedAt: primary, FactorVerifiedAt: factor,
	}, nil
}

func recoverySessionInstant(instant, now time.Time) bool {
	return !instant.IsZero() && instant.Unix() > 0 && !instant.After(now)
}
