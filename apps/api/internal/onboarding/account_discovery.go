package onboarding

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/audit"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"math/big"
	"sort"
	"strings"
	"time"
)

func discoveryCodeHash(browser, code string) []byte {
	sum := sha256.Sum256([]byte("helivanta-account-discovery-v1:" + browser + ":" + code))
	return sum[:]
}

// StartAccountDiscovery sends the same mailbox challenge without any account lookup.
func (s *Service) StartAccountDiscovery(ctx context.Context, in customercontract.DiscoveryStart) error {
	if in.Validate() != nil {
		return ErrInvalid
	}
	if s.store == nil || s.signupMail == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	value, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		return ErrUnavailable
	}
	code := fmt.Sprintf("%08d", value.Int64())
	browser, _ := hex.DecodeString(in.BrowserHash)
	source, _ := hex.DecodeString(in.SourceHash)
	id, _ := uuid.Parse(in.ID)
	err = s.store.ReserveDiscovery(ctx, id, browser, source, strings.ToLower(in.Email), discoveryCodeHash(in.BrowserHash, code), time.Now().UTC())
	if errors.Is(err, store.ErrDiscoveryLimited) {
		return store.ErrDiscoveryLimited
	}
	if err != nil {
		return ErrUnavailable
	}
	if _, err = s.signupMail.SendDiscovery(ctx, in.Email, code); err != nil {
		return ErrUnavailable
	}
	return nil
}

// VerifyAccountDiscovery consumes the code; browser recovery uses the separate list operation.
func (s *Service) VerifyAccountDiscovery(ctx context.Context, in customercontract.DiscoveryProof) error {
	if in.Validate() != nil {
		return ErrInvalid
	}
	if s.store == nil {
		return ErrUnavailable
	}
	browser, _ := hex.DecodeString(in.BrowserHash)
	id, _ := uuid.Parse(in.ID)
	err := s.store.VerifyDiscovery(ctx, id, browser, discoveryCodeHash(in.BrowserHash, in.Code), time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

// AccountChoices discloses native-owned active customer accounts only after mailbox proof.
func (s *Service) AccountChoices(ctx context.Context, in customercontract.DiscoveryAccess) (customercontract.DiscoveryChoices, error) {
	empty := customercontract.DiscoveryChoices{Accounts: []customercontract.DiscoveryChoice{}}
	if in.Validate() != nil {
		return empty, ErrInvalid
	}
	if s.store == nil || s.signupIdentity == nil || s.idp == nil {
		return empty, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	browser, _ := hex.DecodeString(in.BrowserHash)
	id, _ := uuid.Parse(in.ID)
	email, err := s.store.DiscoveryEmail(ctx, id, browser, time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, ErrUnavailable
	}
	accounts, err := s.signupIdentity.DiscoveryAccounts(ctx, email)
	if err != nil {
		return empty, ErrUnavailable
	}
	choices := []customercontract.DiscoveryChoice{}
	for _, account := range accounts {
		if _, err = s.customerIdentityScope(ctx, account.TenantID); errors.Is(err, ErrNotFound) {
			continue
		} else if err != nil {
			return empty, ErrUnavailable
		}
		tenant, _ := uuid.Parse(account.TenantID)
		org, e := s.store.Organization(ctx, tenant)
		if e != nil {
			return empty, ErrUnavailable
		}
		if org.Name == "" || len(org.Name) > 200 || strings.TrimSpace(org.Name) != org.Name {
			return empty, ErrUnavailable
		}
		choices = append(choices, customercontract.DiscoveryChoice{TenantID: account.TenantID, Name: org.Name, Account: email})
	}
	if _, err = s.store.DiscoveryEmail(ctx, id, browser, time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return empty, ErrNotFound
		}
		return empty, ErrUnavailable
	}
	result := customercontract.DiscoveryChoices{Accounts: choices}
	if result.Validate() != nil {
		return empty, ErrUnavailable
	}
	if err = s.store.AppendAudit(ctx, audit.Event{ActorType: audit.ActorSystem, ActorID: customerWorkload, ProductKey: "helivanta", Action: "account.discovery.choices_read", Outcome: audit.OutcomeSuccess, RequestID: in.ID}); err != nil {
		return empty, ErrUnavailable
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].Name == choices[j].Name {
			return choices[i].TenantID < choices[j].TenantID
		}
		return choices[i].Name < choices[j].Name
	})
	return result, nil
}
