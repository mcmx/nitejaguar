package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/mcmx/nitejaguar/common"
	"github.com/mcmx/nitejaguar/ent"
	"github.com/mcmx/nitejaguar/ent/credential"
	"go.jetify.com/typeid"
)

const (
	CredentialScopeTenant = "tenant"
	CredentialScopeGroup  = "group"
	CredentialScopeUser   = "user"

	// CredentialFetchTTLSeconds tells clients how long a just-in-time
	// fetched secret may be held in memory. Clients must not persist it.
	CredentialFetchTTLSeconds = 60
)

// SupportedCredentialTypes are the credential types accepted at creation.
var SupportedCredentialTypes = common.KnownCredentialTypes()

func validCredentialType(t string) bool {
	for _, known := range SupportedCredentialTypes {
		if t == known {
			return true
		}
	}
	return false
}

func validCredentialScope(s string) bool {
	return s == CredentialScopeTenant || s == CredentialScopeGroup || s == CredentialScopeUser
}

func normalizeTenant(tenantID string) string {
	if tenantID == "" {
		return "default"
	}
	return tenantID
}

var (
	credKeyOnce sync.Once
	credKey     []byte
	credKeyErr  error
)

// credentialKey preserves the local backend's established key behavior.
func credentialKey() ([]byte, error) {
	credKeyOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv("CREDENTIALS_KEY"))
		if raw == "" {
			buf := make([]byte, 32)
			if _, err := rand.Read(buf); err != nil {
				credKeyErr = fmt.Errorf("failed to generate ephemeral credentials key: %w", err)
				return
			}
			credKey = buf
			log.Printf("WARNING: CREDENTIALS_KEY is not set; using an ephemeral key. Stored credential secrets will not survive restarts. Set CREDENTIALS_KEY to a stable value for production.")
			return
		}
		if len(raw) == 64 {
			if decoded, err := hexDecode(raw); err == nil {
				credKey = decoded
				return
			}
		}
		if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
			credKey = decoded
			return
		}
		sum := sha256.Sum256([]byte(raw))
		credKey = sum[:]
	})
	if credKeyErr != nil {
		return nil, credKeyErr
	}
	return credKey, nil
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd hex length")
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		var v byte
		for j := 0; j < 2; j++ {
			c := s[i*2+j]
			var n byte
			switch {
			case c >= '0' && c <= '9':
				n = c - '0'
			case c >= 'a' && c <= 'f':
				n = c - 'a' + 10
			case c >= 'A' && c <= 'F':
				n = c - 'A' + 10
			default:
				return nil, fmt.Errorf("invalid hex character")
			}
			v = v*16 + n
		}
		out[i] = v
	}
	return out, nil
}

// CreateCredential stores a new secret encrypted at rest. The plaintext is
// never persisted; only the encrypted envelope is stored.
func (s *service) CreateCredential(tenantID, name, credType, scope, ownerID, secretPlaintext, description string) (*ent.Credential, error) {
	tenantID = normalizeTenant(tenantID)
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	if credType == "" {
		credType = "generic"
	}
	if !validCredentialType(credType) {
		return nil, fmt.Errorf("unknown credential type %q (supported: %s)", credType, strings.Join(SupportedCredentialTypes, ", "))
	}
	if scope == "" {
		scope = CredentialScopeTenant
	}
	if !validCredentialScope(scope) {
		return nil, fmt.Errorf("unknown scope %q (supported: tenant, group, user)", scope)
	}
	if scope == CredentialScopeTenant {
		ownerID = ""
	} else if strings.TrimSpace(ownerID) == "" {
		return nil, fmt.Errorf("owner_id is required for %s scope", scope)
	}
	if err := common.ValidateCredentialSecret(credType, secretPlaintext); err != nil {
		return nil, err
	}

	ctx := context.Background()
	existing, err := s.client.Credential.Query().
		Where(
			credential.TenantID(tenantID),
			credential.Name(name),
			credential.Scope(scope),
			credential.OwnerID(ownerID),
		).
		Only(ctx)
	if err == nil && existing != nil {
		return nil, fmt.Errorf("credential %q already exists in scope %s", name, scope)
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("failed to check existing credential: %w", err)
	}
	if s.credentialCipher == nil {
		return nil, fmt.Errorf("credential encryption is not configured")
	}
	cid, err := typeid.WithPrefix("credential")
	if err != nil {
		return nil, fmt.Errorf("failed to generate credential id: %w", err)
	}
	envelope, err := s.credentialCipher.Encrypt(ctx, tenantID, cid.String(), secretPlaintext)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt credential secret: %w", err)
	}
	row, err := s.client.Credential.Create().
		SetID(cid.String()).
		SetTenantID(tenantID).
		SetName(name).
		SetType(credType).
		SetScope(scope).
		SetOwnerID(ownerID).
		SetSecretEncrypted(envelope).
		SetDescription(description).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create credential: %w", err)
	}
	return row, nil
}

func (s *service) ListCredentials(tenantID string) ([]*ent.Credential, error) {
	q := s.client.Credential.Query()
	if tenantID != "" {
		q.Where(credential.TenantID(tenantID))
	}
	rows, err := q.Order(ent.Asc(credential.FieldName)).All(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to list credentials: %w", err)
	}
	return rows, nil
}

func (s *service) GetCredential(id string) (*ent.Credential, error) {
	row, err := s.client.Credential.Get(context.Background(), id)
	if err != nil {
		return nil, fmt.Errorf("credential not found: %w", err)
	}
	return row, nil
}

func (s *service) DeleteCredential(id string) error {
	if _, err := s.client.Credential.Get(context.Background(), id); err != nil {
		return fmt.Errorf("credential not found: %w", err)
	}
	if err := s.client.Credential.DeleteOneID(id).Exec(context.Background()); err != nil {
		return fmt.Errorf("failed to delete credential: %w", err)
	}
	return nil
}

// ResolveCredential finds a credential within a tenant. Named references
// resolve most-specific-wins: user > group > tenant.
func (s *service) ResolveCredential(tenantID, ref, userID string, groupIDs []string) (*ent.Credential, error) {
	tenantID = normalizeTenant(tenantID)
	if strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("credential reference is required")
	}
	ctx := context.Background()
	if strings.HasPrefix(ref, "credential_") {
		row, err := s.client.Credential.Get(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("credential not found: %w", err)
		}
		if row.TenantID != tenantID {
			return nil, fmt.Errorf("credential not found: %w", err)
		}
		return row, nil
	}
	rows, err := s.client.Credential.Query().
		Where(credential.TenantID(tenantID), credential.Name(ref)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve credential: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("credential not found: %w", fmt.Errorf("%q", ref))
	}
	if userID != "" {
		for _, row := range rows {
			if row.Scope == CredentialScopeUser && row.OwnerID == userID {
				return row, nil
			}
		}
	}
	if len(groupIDs) > 0 {
		groups := make(map[string]struct{}, len(groupIDs))
		for _, group := range groupIDs {
			if group != "" {
				groups[group] = struct{}{}
			}
		}
		for _, row := range rows {
			if row.Scope == CredentialScopeGroup {
				if _, ok := groups[row.OwnerID]; ok {
					return row, nil
				}
			}
		}
	}
	for _, row := range rows {
		if row.Scope == CredentialScopeTenant {
			return row, nil
		}
	}
	return nil, fmt.Errorf("credential not found: %w", fmt.Errorf("%q", ref))
}

func (s *service) DecryptCredentialSecret(row *ent.Credential) (string, error) {
	if row == nil {
		return "", fmt.Errorf("credential is nil")
	}
	if s.credentialCipher == nil {
		return "", fmt.Errorf("credential encryption is not configured")
	}
	return s.credentialCipher.Decrypt(context.Background(), row.TenantID, row.ID, row.SecretEncrypted)
}

// RewrapCredentialSecrets safely migrates older envelopes to the selected
// backend. Saved rows are skipped on restart once their backend and key ref
// match the configured target.
func (s *service) RewrapCredentialSecrets(ctx context.Context, force bool) (int, error) {
	if s.credentialCipher == nil {
		return 0, fmt.Errorf("credential encryption is not configured")
	}
	rows, err := s.client.Credential.Query().Order(ent.Asc(credential.FieldID)).All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to list credentials for rewrap: %w", err)
	}
	updated := 0
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return updated, err
		}
		if !force && !s.credentialCipher.NeedsRewrap(row.SecretEncrypted) {
			continue
		}
		plaintext, err := s.credentialCipher.Decrypt(ctx, row.TenantID, row.ID, row.SecretEncrypted)
		if err != nil {
			return updated, fmt.Errorf("failed to decrypt credential %s during rewrap: %w", row.ID, err)
		}
		envelope, err := s.credentialCipher.Encrypt(ctx, row.TenantID, row.ID, plaintext)
		if err != nil {
			return updated, fmt.Errorf("failed to encrypt credential %s during rewrap: %w", row.ID, err)
		}
		if _, err := s.client.Credential.UpdateOneID(row.ID).SetSecretEncrypted(envelope).Save(ctx); err != nil {
			return updated, fmt.Errorf("failed to save credential %s during rewrap: %w", row.ID, err)
		}
		updated++
	}
	return updated, nil
}
