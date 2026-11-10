package database

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func TestCredentialSecretLocalBackendAndLegacyCompatibility(t *testing.T) {
	t.Setenv("CREDENTIALS_BACKEND", "local")
	t.Setenv("CREDENTIALS_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	env, err := encryptSecret("super-secret")
	if err != nil {
		t.Fatalf("encryptSecret: %v", err)
	}
	if !strings.Contains(env, "\"backend\"") || !strings.Contains(env, "\"ciphertext\"") {
		t.Fatalf("versioned envelope missing metadata: %s", env)
	}
	if got, err := decryptSecret(env); err != nil || got != "super-secret" {
		t.Fatalf("decryptSecret(versioned) = %q, %v; want %q", got, err, "super-secret")
	}

	legacyKey, err := credentialKey()
	if err != nil {
		t.Fatalf("credentialKey: %v", err)
	}
	block, err := aes.NewCipher(legacyKey)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("NewGCM: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("rand.Read(nonce): %v", err)
	}
	legacy := base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte("legacy-secret"), nil))
	if got, err := decryptSecret(legacy); err != nil || got != "legacy-secret" {
		t.Fatalf("decryptSecret(legacy) = %q, %v; want %q", got, err, "legacy-secret")
	}
}

func TestCredentialSecretVaultBackendFailClosedWithoutConfig(t *testing.T) {
	t.Setenv("CREDENTIALS_BACKEND", "vault")
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	if _, err := encryptSecret("should-fail"); err == nil || !strings.Contains(err.Error(), "VAULT_ADDR") {
		t.Fatalf("encryptSecret(vault) = %v, want fail-closed with VAULT_ADDR requirement", err)
	}
}

func TestCredentialSecretExternalBackendRoundTrip(t *testing.T) {
	t.Setenv("CREDENTIALS_BACKEND", "vault")
	t.Setenv("VAULT_ADDR", "https://vault.example.internal")
	t.Setenv("VAULT_TOKEN", "dev-token")
	t.Setenv("VAULT_TRANSIT_KEY", "creds-key")

	env, err := encryptSecret("vault-backed-secret")
	if err != nil {
		t.Fatalf("encryptSecret(vault): %v", err)
	}
	if !strings.Contains(env, "\"backend\":\"vault\"") {
		t.Fatalf("vault envelope missing backend field: %s", env)
	}
	if got, err := decryptSecret(env); err != nil || got != "vault-backed-secret" {
		t.Fatalf("decryptSecret(vault) = %q, %v; want %q", got, err, "vault-backed-secret")
	}
}
