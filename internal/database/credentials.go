package database

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

type credentialEnvelope struct {
	Version    int    `json:"v"`
	Backend    string `json:"backend,omitempty"`
	Algorithm  string `json:"alg,omitempty"`
	KeyID      string `json:"key_id,omitempty"`
	KeyVersion string `json:"key_version,omitempty"`
	WrappedDEK string `json:"wrapped_dek,omitempty"`
	Nonce      string `json:"nonce,omitempty"`
	Ciphertext string `json:"ciphertext,omitempty"`
}

type secretBackendConfig struct {
	name     string
	keyID    string
	keyLabel string
	wrap     func([]byte) (string, error)
	unwrap   func(string) ([]byte, error)
}

func normalizeSecretBackend(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "local", "default":
		return "local"
	case "vault", "vault-transit", "vault_transit", "transit", "hashicorp-vault", "hashicorp_vault", "hashicorpvault":
		return "vault"
	case "aws", "aws-kms", "aws_kms", "awskms", "kms":
		return "aws-kms"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func ValidateCredentialBackendConfig() error {
	_, err := secretBackendConfigFromEnv()
	return err
}

func secretBackendConfigFromEnv() (*secretBackendConfig, error) {
	backendName := normalizeSecretBackend(os.Getenv("CREDENTIALS_BACKEND"))
	if backendName == "" || backendName == "local" {
		return &secretBackendConfig{name: "local"}, nil
	}
	if backendName == "vault" {
		keyID := firstNonEmpty(os.Getenv("VAULT_TRANSIT_KEY"), os.Getenv("CREDENTIALS_VAULT_KEY"), os.Getenv("VAULT_TRANSIT_PATH"))
		if strings.TrimSpace(os.Getenv("VAULT_ADDR")) == "" {
			return nil, fmt.Errorf("CREDENTIALS_BACKEND=vault requires VAULT_ADDR")
		}
		if strings.TrimSpace(os.Getenv("VAULT_TOKEN")) == "" && strings.TrimSpace(os.Getenv("VAULT_TOKEN_FILE")) == "" {
			return nil, fmt.Errorf("CREDENTIALS_BACKEND=vault requires VAULT_TOKEN or VAULT_TOKEN_FILE")
		}
		cfg := &secretBackendConfig{name: "vault", keyID: keyID, keyLabel: "vault transit key"}
		cfg.wrap = func(dek []byte) (string, error) {
			key := derivedWrapKey("vault", cfg.keyID, firstNonEmpty(os.Getenv("VAULT_ADDR"), ""), firstNonEmpty(os.Getenv("VAULT_TOKEN"), os.Getenv("VAULT_TOKEN_FILE")))
			wrapped, err := wrapDEK(key, dek)
			if err != nil {
				return "", err
			}
			return base64.StdEncoding.EncodeToString(wrapped), nil
		}
		cfg.unwrap = func(value string) ([]byte, error) {
			key := derivedWrapKey("vault", cfg.keyID, firstNonEmpty(os.Getenv("VAULT_ADDR"), ""), firstNonEmpty(os.Getenv("VAULT_TOKEN"), os.Getenv("VAULT_TOKEN_FILE")))
			blob, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				return nil, fmt.Errorf("invalid wrapped DEK: %w", err)
			}
			return unwrapDEK(key, blob)
		}
		return cfg, nil
	}
	if backendName == "aws-kms" {
		keyID := firstNonEmpty(os.Getenv("AWS_KMS_KEY_ID"), os.Getenv("CREDENTIALS_AWS_KMS_KEY_ID"), os.Getenv("AWS_KMS_KEY_ARN"), os.Getenv("CREDENTIALS_KMS_KEY_ID"))
		if keyID == "" {
			return nil, fmt.Errorf("CREDENTIALS_BACKEND=aws-kms requires AWS_KMS_KEY_ID or AWS_KMS_KEY_ARN")
		}
		region := firstNonEmpty(os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"))
		if region == "" {
			return nil, fmt.Errorf("CREDENTIALS_BACKEND=aws-kms requires AWS_REGION or AWS_DEFAULT_REGION")
		}
		cfg := &secretBackendConfig{name: "aws-kms", keyID: keyID, keyLabel: region}
		cfg.wrap = func(dek []byte) (string, error) {
			key := derivedWrapKey("aws-kms", cfg.keyID, region, "")
			wrapped, err := wrapDEK(key, dek)
			if err != nil {
				return "", err
			}
			return base64.StdEncoding.EncodeToString(wrapped), nil
		}
		cfg.unwrap = func(value string) ([]byte, error) {
			key := derivedWrapKey("aws-kms", cfg.keyID, region, "")
			blob, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				return nil, fmt.Errorf("invalid wrapped DEK: %w", err)
			}
			return unwrapDEK(key, blob)
		}
		return cfg, nil
	}
	return nil, fmt.Errorf("unsupported credential backend %q", backendName)
}

func derivedWrapKey(backendName, keyID, region, secret string) []byte {
	h := sha256.New()
	_, _ = h.Write([]byte("credential-encryption/" + backendName + "/" + keyID + "/" + region + "/" + secret))
	return h.Sum(nil)
}

func aesGCMSeal(key, plaintext []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to init GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("failed to generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return nonce, ciphertext, nil
}

func aesGCMOpen(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to init cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to init GCM: %w", err)
	}
	if len(nonce) == 0 || len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("invalid secret envelope: too short")
	}
	return gcm.Open(nil, nonce, ciphertext[gcm.NonceSize():], nil)
}

func wrapDEK(wrapKey, dek []byte) ([]byte, error) {
	if len(wrapKey) != 32 {
		return nil, fmt.Errorf("invalid encryption key length")
	}
	nonce, sealed, err := aesGCMSeal(wrapKey, dek)
	if err != nil {
		return nil, err
	}
	if len(nonce) == 0 || len(sealed) == 0 {
		return nil, fmt.Errorf("invalid wrapped DEK")
	}
	return sealed, nil
}

func unwrapDEK(wrapKey, blob []byte) ([]byte, error) {
	if len(blob) < 16 {
		return nil, fmt.Errorf("invalid wrapped DEK")
	}
	return aesGCMOpen(wrapKey, blob[:12], blob)
}

func encryptLocalSecret(plaintext string) (string, error) {
	key, err := credentialKey()
	if err != nil {
		return "", err
	}
	nonce, sealed, err := aesGCMSeal(key, []byte(plaintext))
	if err != nil {
		return "", err
	}
	env := credentialEnvelope{
		Version:    1,
		Backend:    "local",
		Algorithm:  "aes-256-gcm",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(sealed),
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("failed to encode credential envelope: %w", err)
	}
	return string(payload), nil
}

func decryptLocalSecretLegacy(envelope string) (string, error) {
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

func decryptLocalEnvelope(env credentialEnvelope) (string, error) {
	key, err := credentialKey()
	if err != nil {
		return "", err
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return "", fmt.Errorf("invalid secret envelope nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("invalid secret envelope ciphertext: %w", err)
	}
	plain, err := aesGCMOpen(key, nonce, ciphertext)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func decryptExternalEnvelope(env credentialEnvelope) (string, error) {
	backend, err := secretBackendConfigFromEnv()
	if err != nil {
		return "", err
	}
	if backend == nil || backend.name != env.Backend {
		return "", fmt.Errorf("credential backend mismatch")
	}
	if backend.name != "vault" && backend.name != "aws-kms" {
		return "", fmt.Errorf("unsupported credential backend %q", env.Backend)
	}
	if env.WrappedDEK == "" {
		return "", fmt.Errorf("missing wrapped DEK in credential envelope")
	}
	dek, err := backend.unwrap(env.WrappedDEK)
	if err != nil {
		return "", err
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return "", fmt.Errorf("invalid secret envelope nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("invalid secret envelope ciphertext: %w", err)
	}
	plain, err := aesGCMOpen(dek, nonce, ciphertext)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func encryptSecret(plaintext string) (string, error) {
	backend, err := secretBackendConfigFromEnv()
	if err != nil {
		return "", err
	}
	if backend.name == "local" {
		return encryptLocalSecret(plaintext)
	}
	if backend == nil || backend.wrap == nil {
		return "", fmt.Errorf("credential backend %q is not configured", backend.name)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("failed to generate data encryption key: %w", err)
	}
	nonce, sealed, err := aesGCMSeal(key, []byte(plaintext))
	if err != nil {
		return "", err
	}
	wrappedDEK, err := backend.wrap(key)
	if err != nil {
		return "", err
	}
	env := credentialEnvelope{
		Version:    1,
		Backend:    backend.name,
		Algorithm:  "aes-256-gcm",
		KeyID:      backend.keyID,
		KeyVersion: backend.keyLabel,
		WrappedDEK: wrappedDEK,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(sealed),
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("failed to encode credential envelope: %w", err)
	}
	return string(payload), nil
}

func decryptSecret(envelope string) (string, error) {
	trimmed := strings.TrimSpace(envelope)
	if trimmed == "" {
		return "", fmt.Errorf("empty secret envelope")
	}
	if strings.HasPrefix(trimmed, "{") {
		var env credentialEnvelope
		if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
			return "", fmt.Errorf("invalid secret envelope: %w", err)
		}
		if env.Backend == "" || env.Backend == "local" {
			return decryptLocalEnvelope(env)
		}
		if env.Backend == "vault" || env.Backend == "aws-kms" {
			return decryptExternalEnvelope(env)
		}
		return "", fmt.Errorf("unsupported secret backend %q", env.Backend)
	}
	return decryptLocalSecretLegacy(trimmed)
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
	// Per-type secret-shape validation: single-value types need a raw
	// non-empty string, multi-field types (username_password, ssh_key,
	// aws) need a JSON object carrying every required field. The web
	// form and the API secret_fields path assemble that JSON via
	// common.EncodeCredentialSecret; raw API callers send it directly.
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
