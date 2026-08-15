// Package store is the projection and audit ledger on global-postgres. Zitadel
// is the source of truth; nothing here is proof that an org exists.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tesserix/onboarding-service/api/internal/audit"
)

var (
	ErrNotFound            = errors.New("store: not found")
	ErrConflict            = errors.New("store: conflict")
	ErrIdempotencyConflict = errors.New("store: idempotency key reused with a different request")
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const productColumns = `key, display_name, zitadel_project_id, machine_user_id,
	coalesce(oidc_client_id, ''), hosts, identity, tenant_secrets, tenancy,
	roles, data_residency, state, created_at`

func scanProduct(row pgx.Row) (Product, error) {
	var p Product
	err := row.Scan(&p.Key, &p.DisplayName, &p.ZitadelProjectID, &p.MachineUserID,
		&p.OIDCClientID, &p.Hosts, &p.Identity, &p.TenantSecrets, &p.Tenancy,
		&p.Roles, &p.DataResidency, &p.State, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Product{}, ErrNotFound
	}
	return p, err
}

func (s *Store) CreateProduct(ctx context.Context, p Product) error {
	// pgx sends a nil slice as NULL, and both columns are NOT NULL.
	if p.Hosts == nil {
		p.Hosts = []string{}
	}
	if p.Roles == nil {
		p.Roles = []string{}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO products (key, display_name, zitadel_project_id, machine_user_id,
			oidc_client_id, hosts, identity, tenant_secrets, tenancy, roles, data_residency)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		p.Key, p.DisplayName, p.ZitadelProjectID, p.MachineUserID, nullable(p.OIDCClientID),
		p.Hosts, p.Identity, p.TenantSecrets, p.Tenancy, p.Roles, p.DataResidency)
	if err != nil {
		return fmt.Errorf("create product %s: %w", p.Key, err)
	}
	return nil
}

func (s *Store) Product(ctx context.Context, key string) (Product, error) {
	return scanProduct(s.pool.QueryRow(ctx, `SELECT `+productColumns+` FROM products WHERE key = $1`, key))
}

// ProductByMachineUser is how a product's bearer token becomes a scoped
// principal: the token's subject resolves to exactly one product row.
func (s *Store) ProductByMachineUser(ctx context.Context, machineUserID string) (Product, error) {
	return scanProduct(s.pool.QueryRow(ctx,
		`SELECT `+productColumns+` FROM products WHERE machine_user_id = $1 AND state = 'active'`, machineUserID))
}

func (s *Store) Products(ctx context.Context) ([]Product, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+productColumns+` FROM products ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	defer rows.Close()
	return collect(rows, scanProduct)
}

const orgColumns = `o.id, o.zitadel_org_id, o.name, coalesce(o.primary_domain,''), o.state,
	coalesce(o.created_by_product,''), coalesce(o.billing_email,''), coalesce(o.support_email,''),
	coalesce(o.plan,''), coalesce(o.seats,0), coalesce(o.region,''), coalesce(o.external_ref,''),
	o.labels, o.created_at`

func scanOrg(row pgx.Row) (Organization, error) {
	var o Organization
	err := row.Scan(&o.ID, &o.ZitadelOrgID, &o.Name, &o.PrimaryDomain, &o.State,
		&o.CreatedByProduct, &o.BillingEmail, &o.SupportEmail, &o.Plan, &o.Seats,
		&o.Region, &o.ExternalRef, &o.Labels, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	return o, err
}

func (s *Store) CreateOrganization(ctx context.Context, o Organization) (Organization, error) {
	if o.ID == uuid.Nil {
		o.ID = uuid.New()
	}
	if o.Labels == nil {
		o.Labels = map[string]string{}
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO organizations (id, zitadel_org_id, name, primary_domain, created_by_product,
			billing_email, support_email, plan, seats, region, external_ref, labels)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING `+strings.ReplaceAll(orgColumns, "o.", ""),
		o.ID, o.ZitadelOrgID, o.Name, nullable(o.PrimaryDomain), nullable(o.CreatedByProduct),
		nullable(o.BillingEmail), nullable(o.SupportEmail), nullable(o.Plan), nullableInt(o.Seats),
		nullable(o.Region), nullable(o.ExternalRef), o.Labels)
	created, err := scanOrg(row)
	if err != nil {
		return Organization{}, fmt.Errorf("create organization %s: %w", o.Name, err)
	}
	return created, nil
}

func (s *Store) Organization(ctx context.Context, id uuid.UUID) (Organization, error) {
	return scanOrg(s.pool.QueryRow(ctx, `SELECT `+orgColumns+` FROM organizations o WHERE o.id = $1`, id))
}

func (s *Store) OrganizationByZitadelID(ctx context.Context, zitadelOrgID string) (Organization, error) {
	return scanOrg(s.pool.QueryRow(ctx, `SELECT `+orgColumns+` FROM organizations o WHERE o.zitadel_org_id = $1`, zitadelOrgID))
}

// OrganizationByDomain resolves only verified domains. An unverified domain is
// a claim, not evidence, and discovery on a claim is a takeover.
func (s *Store) OrganizationByDomain(ctx context.Context, domain string) (Organization, error) {
	return scanOrg(s.pool.QueryRow(ctx, `
		SELECT `+orgColumns+` FROM organizations o
		JOIN domains d ON d.org_id = o.id
		WHERE d.domain = $1 AND d.state = 'verified'`, domain))
}

func (s *Store) Organizations(ctx context.Context) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+orgColumns+` FROM organizations o ORDER BY o.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()
	return collect(rows, scanOrg)
}

// OrganizationsForProduct is the scoped list: a product sees the orgs holding a
// live grant for it, and nothing else.
func (s *Store) OrganizationsForProduct(ctx context.Context, productKey string) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+orgColumns+` FROM organizations o
		JOIN org_products g ON g.org_id = o.id
		WHERE g.product_key = $1 AND g.revoked_at IS NULL
		ORDER BY o.created_at DESC`, productKey)
	if err != nil {
		return nil, fmt.Errorf("list organizations for %s: %w", productKey, err)
	}
	defer rows.Close()
	return collect(rows, scanOrg)
}

func (s *Store) SetOrganizationState(ctx context.Context, id uuid.UUID, state string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE organizations SET state = $2, updated_at = now() WHERE id = $1`, id, state)
	if err != nil {
		return fmt.Errorf("set organization state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Grant(ctx context.Context, orgID uuid.UUID, productKey string, roles []string) error {
	if roles == nil {
		roles = []string{}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO org_products (org_id, product_key, roles)
		VALUES ($1,$2,$3)
		ON CONFLICT (org_id, product_key)
		DO UPDATE SET roles = EXCLUDED.roles, granted_at = now(), revoked_at = NULL`,
		orgID, productKey, roles)
	if err != nil {
		return fmt.Errorf("grant %s to %s: %w", productKey, orgID, err)
	}
	return nil
}

func (s *Store) RevokeGrant(ctx context.Context, orgID uuid.UUID, productKey string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE org_products SET revoked_at = now()
		WHERE org_id = $1 AND product_key = $2 AND revoked_at IS NULL`, orgID, productKey)
	if err != nil {
		return fmt.Errorf("revoke %s from %s: %w", productKey, orgID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Grants(ctx context.Context, orgID uuid.UUID) ([]Grant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT org_id, product_key, roles, granted_at, revoked_at
		FROM org_products WHERE org_id = $1 ORDER BY product_key`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer rows.Close()
	return collect(rows, func(row pgx.Row) (Grant, error) {
		var g Grant
		err := row.Scan(&g.OrgID, &g.ProductKey, &g.Roles, &g.GrantedAt, &g.RevokedAt)
		return g, err
	})
}

// Entitled is the authorization join. It takes the org id as a string because
// it is called with an unvalidated path parameter; a malformed id is an error,
// never a silent false that a caller might mistake for a clean denial.
func (s *Store) Entitled(ctx context.Context, orgID, productKey string) (bool, error) {
	id, err := uuid.Parse(orgID)
	if err != nil {
		return false, fmt.Errorf("entitled: parse org id: %w", err)
	}
	var exists bool
	err = s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM org_products g
			JOIN organizations o ON o.id = g.org_id
			WHERE g.org_id = $1 AND g.product_key = $2
			  AND g.revoked_at IS NULL AND o.state = 'active')`,
		id, productKey).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("entitled: %w", err)
	}
	return exists, nil
}

func (s *Store) UpsertDomain(ctx context.Context, orgID uuid.UUID, domain, challengeType, challengeValue string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO domains (org_id, domain, challenge_type, challenge_value)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (org_id, domain)
		DO UPDATE SET challenge_type = EXCLUDED.challenge_type,
		              challenge_value = EXCLUDED.challenge_value`,
		orgID, domain, challengeType, challengeValue)
	if err != nil {
		return fmt.Errorf("upsert domain %s: %w", domain, err)
	}
	return nil
}

// MarkDomainVerified fails if another organization already verified the domain:
// the partial unique index is what enforces it, so two API replicas racing
// cannot both win.
func (s *Store) MarkDomainVerified(ctx context.Context, orgID uuid.UUID, domain string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE domains SET state = 'verified', verified_at = now(), last_checked_at = now()
		WHERE org_id = $1 AND domain = $2`, orgID, domain)
	if err != nil {
		return fmt.Errorf("verify domain %s: %w", domain, err)
	}
	return nil
}

func (s *Store) MarkDomainChecked(ctx context.Context, orgID uuid.UUID, domain string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE domains SET last_checked_at = now() WHERE org_id = $1 AND domain = $2`, orgID, domain)
	if err != nil {
		return fmt.Errorf("touch domain %s: %w", domain, err)
	}
	return nil
}

func (s *Store) Domains(ctx context.Context, orgID uuid.UUID) ([]Domain, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT org_id, domain, state, coalesce(challenge_type,''), coalesce(challenge_value,''),
		       last_checked_at, verified_at
		FROM domains WHERE org_id = $1 ORDER BY domain`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()
	return collect(rows, func(row pgx.Row) (Domain, error) {
		var d Domain
		err := row.Scan(&d.OrgID, &d.Domain, &d.State, &d.ChallengeType, &d.ChallengeValue,
			&d.LastCheckedAt, &d.VerifiedAt)
		return d, err
	})
}

func (s *Store) DeleteDomain(ctx context.Context, orgID uuid.UUID, domain string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE org_id = $1 AND domain = $2`, orgID, domain)
	if err != nil {
		return fmt.Errorf("delete domain %s: %w", domain, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpsertIDP(ctx context.Context, idp IDP) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO idps (org_id, zitadel_idp_id, type, display_name, auto_register, state)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id, zitadel_idp_id)
		DO UPDATE SET display_name = EXCLUDED.display_name,
		              auto_register = EXCLUDED.auto_register,
		              state = EXCLUDED.state`,
		idp.OrgID, idp.ZitadelIDPID, idp.Type, idp.DisplayName, idp.AutoRegister, orDefault(idp.State, "active"))
	if err != nil {
		return fmt.Errorf("upsert idp %s: %w", idp.ZitadelIDPID, err)
	}
	return nil
}

func (s *Store) IDPs(ctx context.Context, orgID uuid.UUID) ([]IDP, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT org_id, zitadel_idp_id, type, display_name, auto_register, state, created_at
		FROM idps WHERE org_id = $1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list idps: %w", err)
	}
	defer rows.Close()
	return collect(rows, func(row pgx.Row) (IDP, error) {
		var i IDP
		err := row.Scan(&i.OrgID, &i.ZitadelIDPID, &i.Type, &i.DisplayName, &i.AutoRegister, &i.State, &i.CreatedAt)
		return i, err
	})
}

func (s *Store) DeleteIDP(ctx context.Context, orgID uuid.UUID, idpID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM idps WHERE org_id = $1 AND zitadel_idp_id = $2`, orgID, idpID)
	if err != nil {
		return fmt.Errorf("delete idp %s: %w", idpID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// auditLockKey serializes appends across replicas. The chain has one head, so
// two concurrent appends reading the same tail would claim the same prev_hash
// and the ledger would stop verifying.
const auditLockKey = 8021974

func (s *Store) AppendAudit(ctx context.Context, e audit.Event) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auditLockKey); err != nil {
			return fmt.Errorf("audit lock: %w", err)
		}
		prev := audit.Genesis
		err := tx.QueryRow(ctx, `SELECT hash FROM audit_events ORDER BY seq DESC LIMIT 1`).Scan(&prev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("audit head: %w", err)
		}

		e.PrevHash = prev
		if e.OccurredAt.IsZero() {
			e.OccurredAt = time.Now().UTC()
		}
		if e.Detail == "" {
			e.Detail = "{}"
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO audit_events (occurred_at, actor_type, actor_id, product_key, org_id,
				action, target, outcome, request_id, detail, prev_hash, hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			e.OccurredAt, e.ActorType, e.ActorID, nullable(e.ProductKey), nullableUUID(e.OrgID),
			e.Action, nullable(e.Target), e.Outcome, e.RequestID, e.Detail, e.PrevHash, e.Hash())
		if err != nil {
			return fmt.Errorf("append audit: %w", err)
		}
		return nil
	})
}

func (s *Store) AuditChain(ctx context.Context) ([]audit.Record, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT seq, occurred_at, actor_type, actor_id, coalesce(product_key,''),
		       coalesce(org_id::text,''), action, coalesce(target,''), outcome,
		       request_id, detail, prev_hash, hash
		FROM audit_events ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("read ledger: %w", err)
	}
	defer rows.Close()
	return collect(rows, func(row pgx.Row) (audit.Record, error) {
		var r audit.Record
		err := row.Scan(&r.Seq, &r.OccurredAt, &r.ActorType, &r.ActorID, &r.ProductKey,
			&r.OrgID, &r.Action, &r.Target, &r.Outcome, &r.RequestID, &r.Detail,
			&r.PrevHash, &r.StoredHash)
		return r, err
	})
}

// AuditFilter narrows a ledger read. A zero filter reads everything, so the
// caller — never this method — decides what a principal may see.
type AuditFilter struct {
	OrgID      *uuid.UUID
	ProductKey string
	Actor      string
	Action     string
	RequestID  string
}

func (s *Store) AuditSearch(ctx context.Context, f AuditFilter, limit int) ([]audit.Record, error) {
	where := []string{"TRUE"}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.OrgID != nil {
		add("org_id = $%d", *f.OrgID)
	}
	if f.ProductKey != "" {
		add("product_key = $%d", f.ProductKey)
	}
	if f.Actor != "" {
		add("actor_id = $%d", f.Actor)
	}
	if f.Action != "" {
		add("action = $%d", f.Action)
	}
	if f.RequestID != "" {
		add("request_id = $%d", f.RequestID)
	}
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT seq, occurred_at, actor_type, actor_id, coalesce(product_key,''),
		       coalesce(org_id::text,''), action, coalesce(target,''), outcome,
		       request_id, detail, prev_hash, hash
		FROM audit_events WHERE %s ORDER BY seq DESC LIMIT $%d`,
		strings.Join(where, " AND "), len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("read ledger: %w", err)
	}
	defer rows.Close()
	return collect(rows, func(row pgx.Row) (audit.Record, error) {
		var r audit.Record
		err := row.Scan(&r.Seq, &r.OccurredAt, &r.ActorType, &r.ActorID, &r.ProductKey,
			&r.OrgID, &r.Action, &r.Target, &r.Outcome, &r.RequestID, &r.Detail,
			&r.PrevHash, &r.StoredHash)
		return r, err
	})
}

const idempotencyTTL = 24 * time.Hour

func (s *Store) SaveIdempotent(ctx context.Context, actor, key, requestHash string, status int, body []byte) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO idempotency_keys (actor, key, request_hash, status, response_body, expires_at)
		VALUES ($1,$2,$3,$4,$5,now() + $6::interval)
		ON CONFLICT (actor, key) DO NOTHING`,
		actor, key, requestHash, status, body, idempotencyTTL.String())
	if err != nil {
		return false, fmt.Errorf("save idempotency key: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Idempotent returns the first response for this actor and key. A key reused
// with a different request is a caller bug and must not replay someone else's
// result, so it is a conflict rather than a hit.
func (s *Store) Idempotent(ctx context.Context, actor, key, requestHash string) (IdempotentResponse, error) {
	var got IdempotentResponse
	var storedHash string
	err := s.pool.QueryRow(ctx, `
		SELECT request_hash, status, response_body FROM idempotency_keys
		WHERE actor = $1 AND key = $2 AND expires_at > now()`, actor, key).
		Scan(&storedHash, &got.Status, &got.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return IdempotentResponse{}, ErrNotFound
	}
	if err != nil {
		return IdempotentResponse{}, fmt.Errorf("read idempotency key: %w", err)
	}
	if storedHash != requestHash {
		return IdempotentResponse{}, ErrIdempotencyConflict
	}
	return got, nil
}

func (s *Store) PurgeExpiredIdempotencyKeys(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE expires_at < now()`)
	if err != nil {
		return fmt.Errorf("purge idempotency keys: %w", err)
	}
	return nil
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func collect[T any](rows pgx.Rows, scan func(pgx.Row) (T, error)) ([]T, error) {
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

func nullableUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
