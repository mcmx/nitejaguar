package database

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/mcmx/nitejaguar/ent"
)

func TestCredentialCipherLocalAndLegacyCompatibility(t *testing.T) {
	t.Setenv("CREDENTIALS_BACKEND", "local")
	t.Setenv("CREDENTIALS_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	cipher, err := newCredentialCipherFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	envelope, err := cipher.Encrypt(context.Background(), "acme", "credential_test", "super-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(envelope, "super-secret") {
		t.Fatalf("envelope contains plaintext: %s", envelope)
	}
	var decoded encryptedCredentialEnvelope
	if err := json.Unmarshal([]byte(envelope), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 2 || decoded.Backend != "local" {
		t.Fatalf("envelope metadata = %#v; want version 2 local", decoded)
	}
	if got, err := cipher.Decrypt(context.Background(), "acme", "credential_test", envelope); err != nil || got != "super-secret" {
		t.Fatalf("Decrypt = %q, %v; want super-secret", got, err)
	}
	if _, err := cipher.Decrypt(context.Background(), "other-tenant", "credential_test", envelope); err == nil {
		t.Fatal("decrypt accepted an envelope moved to a different tenant")
	}

	tampered := strings.Replace(envelope, `"ciphertext":"`, `"ciphertext":"A`, 1)
	if _, err := cipher.Decrypt(context.Background(), "acme", "credential_test", tampered); err == nil {
		t.Fatal("decrypt accepted a tampered envelope")
	}

	legacyKey, err := credentialKey()
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipherBlockGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	legacy := base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte("legacy-secret"), nil))
	if got, err := cipher.Decrypt(context.Background(), "acme", "credential_legacy", legacy); err != nil || got != "legacy-secret" {
		t.Fatalf("legacy Decrypt = %q, %v; want legacy-secret", got, err)
	}
	oldNonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(oldNonce); err != nil {
		t.Fatal(err)
	}
	oldVersioned, err := json.Marshal(encryptedCredentialEnvelope{
		Version:    1,
		Backend:    "local",
		Algorithm:  "aes-256-gcm",
		Nonce:      base64.StdEncoding.EncodeToString(oldNonce),
		Ciphertext: base64.StdEncoding.EncodeToString(gcm.Seal(oldNonce, oldNonce, []byte("legacy-secret"), nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cipher.Decrypt(context.Background(), "acme", "credential_old_version", string(oldVersioned)); err != nil || got != "legacy-secret" {
		t.Fatalf("versioned legacy Decrypt = %q, %v; want legacy-secret", got, err)
	}
}

func TestCredentialBackendConfigFailsClosed(t *testing.T) {
	t.Setenv("CREDENTIALS_KEY", "local-key-must-not-be-a-fallback")
	t.Setenv("CREDENTIALS_BACKEND", "vault")
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VAULT_TOKEN_FILE", "")
	if _, err := newCredentialCipherFromEnv(context.Background()); err == nil {
		t.Fatal("Vault config unexpectedly fell back to the local key")
	}
	if _, err := New(); err == nil {
		t.Fatal("database startup accepted an invalid Vault configuration")
	}

	t.Setenv("CREDENTIALS_BACKEND", "aws-kms")
	t.Setenv("AWS_KMS_KEY_ID", "")
	t.Setenv("AWS_KMS_KEY_ARN", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	if _, err := newCredentialCipherFromEnv(context.Background()); err == nil {
		t.Fatal("AWS KMS config unexpectedly fell back to the local key")
	}
	if _, err := New(); err == nil {
		t.Fatal("database startup accepted an invalid AWS KMS configuration")
	}
}

func TestCredentialCipherVaultTransitWrapsOnlyDEK(t *testing.T) {
	const secret = "never-send-this-secret-to-vault"
	wrapKey := []byte("0123456789abcdef0123456789abcdef")
	var seenContext string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Vault-Token"); got != "test-token" {
			t.Errorf("Vault token header = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if strings.Contains(string(body), secret) {
			t.Errorf("Vault received credential plaintext")
		}
		switch r.URL.Path {
		case "/v1/transit/encrypt/credentials":
			var request struct {
				Plaintext string `json:"plaintext"`
				Context   string `json:"context"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Errorf("decode encrypt request: %v", err)
				return
			}
			dek, err := base64.StdEncoding.DecodeString(request.Plaintext)
			if err != nil || len(dek) != 32 {
				t.Errorf("Vault plaintext is not a 32-byte DEK")
				return
			}
			seenContext = request.Context
			wrapped, err := sealTestKey(wrapKey, dek, []byte(request.Context))
			if err != nil {
				t.Errorf("wrap DEK: %v", err)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{
				"ciphertext": "vault:v3:" + base64.StdEncoding.EncodeToString(wrapped),
			}})
		case "/v1/transit/decrypt/credentials":
			var request struct {
				Ciphertext string `json:"ciphertext"`
				Context    string `json:"context"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Errorf("decode decrypt request: %v", err)
				return
			}
			encoded := strings.TrimPrefix(request.Ciphertext, "vault:v3:")
			wrapped, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Errorf("decode wrapped key: %v", err)
				return
			}
			dek, err := openTestKey(wrapKey, wrapped, []byte(request.Context))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{
				"plaintext": base64.StdEncoding.EncodeToString(dek),
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("VAULT_ADDR", server.URL)
	t.Setenv("VAULT_TOKEN", "test-token")
	t.Setenv("VAULT_TRANSIT_KEY", "credentials")
	vault, keyRef, err := newVaultTransitFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cipher := &envelopeCipher{backend: "vault", keyRef: keyRef, wrapper: vault}
	envelope, err := cipher.Encrypt(context.Background(), "acme", "credential_123", secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if seenContext == "" {
		t.Fatal("Vault encrypt did not receive credential identity context")
	}
	var decoded encryptedCredentialEnvelope
	if err := json.Unmarshal([]byte(envelope), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.KeyVersion != "vault:v3" {
		t.Fatalf("key version = %q, want vault:v3", decoded.KeyVersion)
	}
	if got, err := cipher.Decrypt(context.Background(), "acme", "credential_123", envelope); err != nil || got != secret {
		t.Fatalf("Decrypt = %q, %v; want original secret", got, err)
	}
	if _, err := cipher.Decrypt(context.Background(), "acme", "credential_moved", envelope); err == nil {
		t.Fatal("Vault decrypt accepted an envelope moved to a different credential")
	}
}

func TestCredentialCipherVaultErrorsDoNotLeakOrFallBack(t *testing.T) {
	const sensitive = "provider-error-secret-sentinel"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, sensitive, http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("VAULT_ADDR", server.URL)
	t.Setenv("VAULT_TOKEN", "test-token")
	t.Setenv("VAULT_TRANSIT_KEY", "credentials")
	vault, keyRef, err := newVaultTransitFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cipher := &envelopeCipher{backend: "vault", keyRef: keyRef, wrapper: vault}
	_, err = cipher.Encrypt(context.Background(), "acme", "credential_123", sensitive)
	if err == nil || strings.Contains(err.Error(), sensitive) {
		t.Fatalf("Encrypt error = %v; want sanitized provider error", err)
	}
}

func TestCredentialCipherAWSKMSWrapsOnlyDEKAndUsesContext(t *testing.T) {
	const secret = "never-send-this-secret-to-kms"
	mock := &mockKMS{wrapKey: []byte("0123456789abcdef0123456789abcdef")}
	cipher := &envelopeCipher{backend: "aws-kms", keyRef: "alias/credentials", wrapper: &awsKMS{client: mock, keyID: "alias/credentials"}}
	envelope, err := cipher.Encrypt(context.Background(), "acme", "credential_123", secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if mock.wrapCalls != 1 || mock.lastPlaintextLen != 32 {
		t.Fatalf("KMS Encrypt calls=%d plaintext length=%d, want one call with only a DEK", mock.wrapCalls, mock.lastPlaintextLen)
	}
	if mock.lastContext["nitejaguar_tenant_id"] != "acme" || mock.lastContext["nitejaguar_credential_id"] != "credential_123" {
		t.Fatalf("KMS encryption context = %#v", mock.lastContext)
	}
	if got, err := cipher.Decrypt(context.Background(), "acme", "credential_123", envelope); err != nil || got != secret {
		t.Fatalf("Decrypt = %q, %v; want original secret", got, err)
	}
	if mock.lastDecryptKeyID != "arn:aws:kms:region:account:key/key-id" {
		t.Fatalf("KMS decrypt key = %q; want key ID recorded by Encrypt", mock.lastDecryptKeyID)
	}
	if _, err := cipher.Decrypt(context.Background(), "other", "credential_123", envelope); err == nil {
		t.Fatal("KMS decrypt accepted a changed encryption context")
	}
}

func TestCredentialCipherAWSKMSErrorsAreSanitized(t *testing.T) {
	mock := &mockKMS{fail: true}
	wrapper := &awsKMS{client: mock, keyID: "alias/credentials"}
	_, _, err := wrapper.Wrap(context.Background(), []byte("key-material-sentinel"), map[string]string{
		"tenant_id": "acme", "credential_id": "credential_123",
	})
	if err == nil || strings.Contains(err.Error(), "key-material-sentinel") {
		t.Fatalf("KMS error = %v; want sanitized provider error", err)
	}
}

func TestCredentialRewrapResumesAfterProviderError(t *testing.T) {
	t.Setenv("CREDENTIALS_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	localKey, err := credentialKey()
	if err != nil {
		t.Fatal(err)
	}
	driver, rawDB, err := openTursoEnt("file:credential_rewrap?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatal(err)
	}
	client := ent.NewClient(ent.Driver(driver))
	defer func() {
		_ = client.Close()
		_ = rawDB.Close()
	}()
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := &service{
		client:           client,
		credentialCipher: &envelopeCipher{backend: "local", local: localKey},
	}
	first, err := service.CreateCredential("default", "first", "token", "", "", "first-secret", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateCredential("default", "second", "token", "", "", "second-secret", "")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := sealTestKey(localKey, []byte("second-secret"), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err = client.Credential.UpdateOneID(second.ID).
		SetSecretEncrypted(base64.StdEncoding.EncodeToString(legacy)).
		Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wrapper := &testDataKeyWrapper{
		key:    []byte("0123456789abcdef0123456789abcdef"),
		failAt: 2,
	}
	service.credentialCipher = &envelopeCipher{backend: "aws-kms", keyRef: "alias/new-key", wrapper: wrapper}

	if count, err := service.RewrapCredentialSecrets(context.Background(), false); err == nil || count != 1 {
		t.Fatalf("first rewrap = (%d, %v), want one saved row and provider error", count, err)
	}
	wrapper.failAt = 0
	wrapper.calls = 0
	if count, err := service.RewrapCredentialSecrets(context.Background(), false); err != nil || count != 1 {
		t.Fatalf("resumed rewrap = (%d, %v), want one remaining row", count, err)
	}
	if count, err := service.RewrapCredentialSecrets(context.Background(), false); err != nil || count != 0 {
		t.Fatalf("completed rewrap = (%d, %v), want no remaining rows", count, err)
	}
	for _, row := range []*ent.Credential{first, second} {
		stored, err := client.Credential.Get(context.Background(), row.ID)
		if err != nil {
			t.Fatal(err)
		}
		plaintext, err := service.DecryptCredentialSecret(stored)
		if err != nil || plaintext != row.Name+"-secret" {
			t.Fatalf("decrypted %s = %q, %v", row.Name, plaintext, err)
		}
	}
}

type mockKMS struct {
	wrapKey          []byte
	wrapCalls        int
	lastPlaintextLen int
	lastContext      map[string]string
	lastDecryptKeyID string
	fail             bool
}

type testDataKeyWrapper struct {
	key    []byte
	calls  int
	failAt int
}

func (w *testDataKeyWrapper) Wrap(_ context.Context, dek []byte, identity map[string]string) ([]byte, string, error) {
	w.calls++
	if w.failAt > 0 && w.calls == w.failAt {
		return nil, "", errors.New("provider unavailable")
	}
	wrapped, err := sealTestKey(w.key, dek, contextAAD(identity))
	return wrapped, "test-version", err
}

func (w *testDataKeyWrapper) Unwrap(_ context.Context, wrapped []byte, identity map[string]string, _, _ string) ([]byte, string, error) {
	dek, err := openTestKey(w.key, wrapped, contextAAD(identity))
	return dek, "test-version", err
}

func (m *mockKMS) Encrypt(_ context.Context, input *kms.EncryptInput, _ ...func(*kms.Options)) (*kms.EncryptOutput, error) {
	if m.fail {
		return nil, errors.New("provider error includes key-material-sentinel")
	}
	m.wrapCalls++
	m.lastPlaintextLen = len(input.Plaintext)
	m.lastContext = input.EncryptionContext
	ciphertext, err := sealTestKey(m.wrapKey, input.Plaintext, contextAAD(input.EncryptionContext))
	if err != nil {
		return nil, err
	}
	return &kms.EncryptOutput{CiphertextBlob: ciphertext, KeyId: aws.String("arn:aws:kms:region:account:key/key-id")}, nil
}

func (m *mockKMS) Decrypt(_ context.Context, input *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	if m.fail {
		return nil, errors.New("provider error includes key-material-sentinel")
	}
	if input.KeyId != nil {
		m.lastDecryptKeyID = *input.KeyId
	}
	plaintext, err := openTestKey(m.wrapKey, input.CiphertextBlob, contextAAD(input.EncryptionContext))
	if err != nil {
		return nil, err
	}
	return &kms.DecryptOutput{Plaintext: plaintext, KeyId: aws.String("arn:aws:kms:region:account:key/key-id")}, nil
}

func sealTestKey(key, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

func openTestKey(key, sealed, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("short ciphertext")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], aad)
}

func cipherBlockGCM(block cipher.Block) (cipher.AEAD, error) {
	return cipher.NewGCM(block)
}
