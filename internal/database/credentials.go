package database

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
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

// Credential scopes and types for the credentials model (roadmap slice 3).
const (
	CredentialScopeTenant = "tenant"
	CredentialScopeGroup  = "group"
	CredentialScopeUser   = "user"

	// CredentialFetchTTLSeconds tells clients how long a just-in-time
	// fetched secret may be held in memory. Clients must not persist it.
	CredentialFetchTTLSeconds = 60
)

// SupportedCredentialTypes are the credential types accepted at creation.
// The provider-collection registry in common/ is the source of truth:
// generic families plus each collection's declared type. The legacy `s3`
// type is rejected — S3 lives in the AWS collection and uses `aws`.
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

// credentialKey returns the AES-256 key used for secret-at-rest encryption.
// Key source: CREDENTIALS_KEY env (64-char hex, base64, or any passphrase
// hashed with SHA-256). When unset, an ephemeral process-local key is
// generated so the server still runs; secrets then do not survive restarts.
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

// encryptSecret seals plaintext with AES-GCM. Output is base64(nonce|ciphertext).
func encryptSecret(plaintext string) (string, error) {
	key, err := credentialKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to init GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptSecret opens a base64(nonce|ciphertext) envelope from encryptSecret.
func decryptSecret(envelope string) (string, error) {
	key, err := credentialKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(envelope)
	if err != nil {
		return "", fmt.Errorf("invalid secret envelope: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to init GCM: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("invalid secret envelope: too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt secret: %w", err)
	}
	return string(plain), nil
}

// CreateCredential stores a new secret encrypted at rest. The plaintext is
// never persisted; only the AES-GCM envelope is stored. Names are unique
// per (tenant, name, scope, owner).
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
	if secretPlaintext == "" {
		return nil, fmt.Errorf("secret is required")
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
	envelope, err := encryptSecret(secretPlaintext)
	if err != nil {
		return nil, err
	}
	cid, _ := typeid.WithPrefix("credential")
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

// ListCredentials returns credential rows for a tenant (empty tenantID lists
// all). Callers must never expose SecretEncrypted.
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

// GetCredential returns a single credential row by id.
func (s *service) GetCredential(id string) (*ent.Credential, error) {
	row, err := s.client.Credential.Get(context.Background(), id)
	if err != nil {
		return nil, fmt.Errorf("credential not found: %w", err)
	}
	return row, nil
}

// DeleteCredential removes a credential row by id.
func (s *service) DeleteCredential(id string) error {
	if _, err := s.client.Credential.Get(context.Background(), id); err != nil {
		return fmt.Errorf("credential not found: %w", err)
	}
	if err := s.client.Credential.DeleteOneID(id).Exec(context.Background()); err != nil {
		return fmt.Errorf("failed to delete credential: %w", err)
	}
	return nil
}

// ResolveCredential finds the credential for a node reference within a
// tenant. An id reference (credential_ prefix) resolves directly; a name
// reference resolves most-specific-wins: user > group > tenant.
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
		for _, g := range groupIDs {
			if g != "" {
				groups[g] = struct{}{}
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
	// Named credentials exist in this tenant but none match the caller's
	// identity (e.g. only user-scoped rows and no user_id given).
	return nil, fmt.Errorf("credential not found: %w", fmt.Errorf("%q", ref))
}

// DecryptCredentialSecret opens the stored envelope. It must only be called
// on the just-in-time fetch path; the plaintext must never be logged or
// persisted.
func (s *service) DecryptCredentialSecret(row *ent.Credential) (string, error) {
	if row == nil {
		return "", fmt.Errorf("credential is nil")
	}
	return decryptSecret(row.SecretEncrypted)
}
