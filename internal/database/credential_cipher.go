package database

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

type credentialCipher interface {
	Encrypt(context.Context, string, string, string) (string, error)
	Decrypt(context.Context, string, string, string) (string, error)
	NeedsRewrap(string) bool
}

type dataKeyWrapper interface {
	Wrap(context.Context, []byte, map[string]string) ([]byte, string, error)
	Unwrap(context.Context, []byte, map[string]string, string, string) ([]byte, string, error)
}

type encryptedCredentialEnvelope struct {
	Version    int    `json:"v"`
	Backend    string `json:"backend"`
	Algorithm  string `json:"alg"`
	KeyRef     string `json:"key_ref,omitempty"`
	KeyVersion string `json:"key_version,omitempty"`
	WrappedDEK string `json:"wrapped_dek,omitempty"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type envelopeCipher struct {
	backend string
	keyRef  string
	local   []byte
	wrapper dataKeyWrapper
}

func newCredentialCipherFromEnv(ctx context.Context) (credentialCipher, error) {
	backend := selectedCredentialBackend(os.Getenv("CREDENTIALS_BACKEND"))
	switch backend {
	case "local":
		key, err := credentialKey()
		if err != nil {
			return nil, err
		}
		return &envelopeCipher{backend: "local", local: key}, nil
	case "vault":
		wrapper, keyRef, err := newVaultTransitFromEnv()
		if err != nil {
			return nil, err
		}
		return &envelopeCipher{backend: "vault", keyRef: keyRef, wrapper: wrapper}, nil
	case "aws-kms":
		wrapper, keyRef, err := newAWSKMSFromEnv(ctx)
		if err != nil {
			return nil, err
		}
		return &envelopeCipher{backend: "aws-kms", keyRef: keyRef, wrapper: wrapper}, nil
	default:
		return nil, fmt.Errorf("unsupported credentials backend %q", backend)
	}
}

func selectedCredentialBackend(name string) string {
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

func credentialEncryptionContext(tenantID, credentialID string) map[string]string {
	return map[string]string{
		"tenant_id":     normalizeTenant(tenantID),
		"credential_id": credentialID,
	}
}

func contextAAD(values map[string]string) []byte {
	encoded, _ := json.Marshal(values)
	return encoded
}

func (c *envelopeCipher) Encrypt(ctx context.Context, tenantID, credentialID, plaintext string) (string, error) {
	identity := credentialEncryptionContext(tenantID, credentialID)
	dek := c.local
	if c.backend != "local" {
		if c.wrapper == nil {
			return "", fmt.Errorf("credential backend is not configured")
		}
		dek = make([]byte, 32)
		if _, err := rand.Read(dek); err != nil {
			return "", fmt.Errorf("failed to generate data encryption key")
		}
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return "", fmt.Errorf("failed to initialize credential encryption")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to initialize credential encryption")
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate credential encryption nonce")
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), contextAAD(identity))

	env := encryptedCredentialEnvelope{
		Version:    2,
		Backend:    c.backend,
		Algorithm:  "aes-256-gcm",
		KeyRef:     c.keyRef,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(sealed),
	}
	if c.backend != "local" {
		wrapped, keyVersion, err := c.wrapper.Wrap(ctx, dek, identity)
		if err != nil {
			return "", fmt.Errorf("credential key wrapping failed: %w", err)
		}
		if len(wrapped) == 0 {
			return "", fmt.Errorf("credential key wrapping returned an empty key")
		}
		env.WrappedDEK = base64.StdEncoding.EncodeToString(wrapped)
		env.KeyVersion = keyVersion
	}
	encoded, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("failed to encode credential envelope")
	}
	return string(encoded), nil
}

func (c *envelopeCipher) Decrypt(ctx context.Context, tenantID, credentialID, envelope string) (string, error) {
	trimmed := strings.TrimSpace(envelope)
	if trimmed == "" {
		return "", fmt.Errorf("empty credential envelope")
	}
	if !strings.HasPrefix(trimmed, "{") {
		key, err := credentialKey()
		if err != nil {
			return "", err
		}
		return decryptLegacyLocalSecret(key, trimmed)
	}

	var env encryptedCredentialEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return "", fmt.Errorf("invalid credential envelope")
	}
	if (env.Version != 1 && env.Version != 2) || env.Algorithm != "aes-256-gcm" {
		return "", fmt.Errorf("unsupported credential envelope version or algorithm")
	}
	if env.Version == 1 && env.Backend != "local" {
		return "", fmt.Errorf("unsupported legacy credential envelope")
	}
	identity := credentialEncryptionContext(tenantID, credentialID)
	var dek []byte
	switch env.Backend {
	case "local":
		key, err := credentialKey()
		if err != nil {
			return "", err
		}
		dek = key
	case "vault", "aws-kms":
		if env.Backend != c.backend || c.wrapper == nil {
			return "", fmt.Errorf("credential envelope backend does not match configured backend")
		}
		wrapped, err := base64.StdEncoding.DecodeString(env.WrappedDEK)
		if err != nil {
			return "", fmt.Errorf("invalid wrapped credential key")
		}
		dek, _, err = c.wrapper.Unwrap(ctx, wrapped, identity, env.KeyRef, env.KeyVersion)
		if err != nil {
			return "", fmt.Errorf("credential key unwrapping failed: %w", err)
		}
	default:
		return "", fmt.Errorf("unsupported credential envelope backend")
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return "", fmt.Errorf("invalid credential envelope nonce")
	}
	sealed, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("invalid credential envelope ciphertext")
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return "", fmt.Errorf("failed to initialize credential decryption")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to initialize credential decryption")
	}
	var plaintext []byte
	if env.Version == 1 && env.Backend == "local" && env.WrappedDEK == "" && len(sealed) >= gcm.NonceSize() && bytes.Equal(sealed[:gcm.NonceSize()], nonce) {
		// Version 1 was emitted by the initial backend-selection prototype
		// before identity binding was added.
		plaintext, err = gcm.Open(nil, nonce, sealed[gcm.NonceSize():], nil)
	} else if env.Version == 1 {
		return "", fmt.Errorf("unsupported legacy credential envelope")
	} else {
		plaintext, err = gcm.Open(nil, nonce, sealed, contextAAD(identity))
	}
	if err != nil {
		return "", fmt.Errorf("failed to decrypt credential secret")
	}
	return string(plaintext), nil
}

func (c *envelopeCipher) NeedsRewrap(envelope string) bool {
	if !strings.HasPrefix(strings.TrimSpace(envelope), "{") {
		return c.backend != "local"
	}
	var env encryptedCredentialEnvelope
	if err := json.Unmarshal([]byte(envelope), &env); err != nil {
		return true
	}
	return env.Version != 2 || env.Backend != c.backend || env.KeyRef != c.keyRef
}

func decryptLegacyLocalSecret(key []byte, envelope string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(envelope)
	if err != nil {
		return "", fmt.Errorf("invalid legacy credential envelope")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to initialize credential decryption")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to initialize credential decryption")
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("invalid legacy credential envelope")
	}
	plaintext, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt legacy credential secret")
	}
	return string(plaintext), nil
}

type vaultTransit struct {
	address   *url.URL
	mount     string
	key       string
	token     string
	namespace string
	client    *http.Client
}

func newVaultTransitFromEnv() (*vaultTransit, string, error) {
	rawAddress := strings.TrimSpace(os.Getenv("VAULT_ADDR"))
	if rawAddress == "" {
		return nil, "", fmt.Errorf("CREDENTIALS_BACKEND=vault requires VAULT_ADDR")
	}
	address, err := url.Parse(rawAddress)
	if err != nil || (address.Scheme != "https" && address.Scheme != "http") || address.Host == "" {
		return nil, "", fmt.Errorf("VAULT_ADDR must be an absolute HTTP(S) URL")
	}
	if address.User != nil || address.RawQuery != "" || address.Fragment != "" {
		return nil, "", fmt.Errorf("VAULT_ADDR must not contain credentials, query, or fragment")
	}
	token := strings.TrimSpace(os.Getenv("VAULT_TOKEN"))
	if token == "" {
		tokenPath := strings.TrimSpace(os.Getenv("VAULT_TOKEN_FILE"))
		if tokenPath == "" {
			return nil, "", fmt.Errorf("CREDENTIALS_BACKEND=vault requires VAULT_TOKEN or VAULT_TOKEN_FILE")
		}
		tokenBytes, err := os.ReadFile(tokenPath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read VAULT_TOKEN_FILE")
		}
		token = strings.TrimSpace(string(tokenBytes))
		if token == "" {
			return nil, "", fmt.Errorf("VAULT_TOKEN_FILE is empty")
		}
	}
	key := strings.Trim(strings.TrimSpace(os.Getenv("VAULT_TRANSIT_KEY")), "/")
	if !validTransitKey(key) {
		return nil, "", fmt.Errorf("CREDENTIALS_BACKEND=vault requires VAULT_TRANSIT_KEY")
	}
	mount := strings.Trim(strings.TrimSpace(os.Getenv("VAULT_TRANSIT_MOUNT")), "/")
	if mount == "" {
		mount = "transit"
	}
	if !validTransitMount(mount) {
		return nil, "", fmt.Errorf("VAULT_TRANSIT_MOUNT contains an invalid path")
	}
	v := &vaultTransit{
		address: address, mount: mount, key: key, token: token,
		namespace: strings.TrimSpace(os.Getenv("VAULT_NAMESPACE")),
		client:    &http.Client{Timeout: 10 * time.Second},
	}
	return v, key, nil
}

func validTransitKey(key string) bool {
	return key != "" && key != "." && key != ".." && !strings.ContainsAny(key, "/\\?#")
}

func validTransitMount(mount string) bool {
	if mount == "" || strings.ContainsAny(mount, "\\?#") {
		return false
	}
	for _, segment := range strings.Split(mount, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func (v *vaultTransit) request(ctx context.Context, operation, key string, payload any) ([]byte, error) {
	if !validTransitKey(key) {
		return nil, fmt.Errorf("invalid Vault Transit key reference")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare Vault Transit request")
	}
	endpoint := *v.address
	endpoint.Path = path.Join(endpoint.Path, "/v1", v.mount, operation, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to prepare Vault Transit request")
	}
	req.Header.Set("X-Vault-Token", v.token)
	req.Header.Set("Content-Type", "application/json")
	if v.namespace != "" {
		req.Header.Set("X-Vault-Namespace", v.namespace)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault transit %s request failed", operation)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("vault transit %s request failed with status %d", operation, resp.StatusCode)
	}
	const maxResponseSize = 1 << 20
	response, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil || len(response) > maxResponseSize {
		return nil, fmt.Errorf("invalid Vault Transit response")
	}
	return response, nil
}

func transitContext(values map[string]string) string {
	return base64.StdEncoding.EncodeToString(contextAAD(values))
}

func (v *vaultTransit) Wrap(ctx context.Context, plaintext []byte, identity map[string]string) ([]byte, string, error) {
	response, err := v.request(ctx, "encrypt", v.key, map[string]string{
		"plaintext": base64.StdEncoding.EncodeToString(plaintext),
		"context":   transitContext(identity),
	})
	if err != nil {
		return nil, "", err
	}
	var result struct {
		Data struct {
			Ciphertext string `json:"ciphertext"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &result); err != nil || result.Data.Ciphertext == "" {
		return nil, "", fmt.Errorf("invalid Vault Transit encrypt response")
	}
	encoded := result.Data.Ciphertext
	version := ""
	if strings.HasPrefix(encoded, "vault:v") {
		if colon := strings.Index(encoded[7:], ":"); colon >= 0 {
			version = encoded[:7+colon]
		}
	}
	return []byte(encoded), version, nil
}

func (v *vaultTransit) Unwrap(ctx context.Context, ciphertext []byte, identity map[string]string, keyRef, _ string) ([]byte, string, error) {
	if !validTransitKey(keyRef) {
		return nil, "", fmt.Errorf("credential envelope is missing the Vault key reference")
	}
	response, err := v.request(ctx, "decrypt", keyRef, map[string]string{
		"ciphertext": string(ciphertext),
		"context":    transitContext(identity),
	})
	if err != nil {
		return nil, "", err
	}
	var result struct {
		Data struct {
			Plaintext string `json:"plaintext"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &result); err != nil || result.Data.Plaintext == "" {
		return nil, "", fmt.Errorf("invalid Vault Transit decrypt response")
	}
	plaintext, err := base64.StdEncoding.DecodeString(result.Data.Plaintext)
	if err != nil {
		return nil, "", fmt.Errorf("invalid Vault Transit decrypt response")
	}
	return plaintext, "", nil
}

type kmsAPI interface {
	Encrypt(context.Context, *kms.EncryptInput, ...func(*kms.Options)) (*kms.EncryptOutput, error)
	Decrypt(context.Context, *kms.DecryptInput, ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

type awsKMS struct {
	client kmsAPI
	keyID  string
}

func newAWSKMSFromEnv(ctx context.Context) (*awsKMS, string, error) {
	keyID := strings.TrimSpace(os.Getenv("AWS_KMS_KEY_ID"))
	if keyID == "" {
		keyID = strings.TrimSpace(os.Getenv("AWS_KMS_KEY_ARN"))
	}
	if keyID == "" {
		return nil, "", fmt.Errorf("CREDENTIALS_BACKEND=aws-kms requires AWS_KMS_KEY_ID or AWS_KMS_KEY_ARN")
	}
	region := strings.TrimSpace(os.Getenv("AWS_REGION"))
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION"))
	}
	if region == "" {
		return nil, "", fmt.Errorf("CREDENTIALS_BACKEND=aws-kms requires AWS_REGION or AWS_DEFAULT_REGION")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, "", fmt.Errorf("failed to configure AWS credential provider chain")
	}
	return &awsKMS{client: kms.NewFromConfig(cfg), keyID: keyID}, keyID, nil
}

func kmsEncryptionContext(identity map[string]string) map[string]string {
	return map[string]string{
		"nitejaguar_tenant_id":     identity["tenant_id"],
		"nitejaguar_credential_id": identity["credential_id"],
	}
}

func (k *awsKMS) Wrap(ctx context.Context, plaintext []byte, identity map[string]string) ([]byte, string, error) {
	result, err := k.client.Encrypt(ctx, &kms.EncryptInput{
		KeyId:             aws.String(k.keyID),
		Plaintext:         plaintext,
		EncryptionContext: kmsEncryptionContext(identity),
	})
	if err != nil {
		return nil, "", fmt.Errorf("AWS KMS encrypt request failed")
	}
	if len(result.CiphertextBlob) == 0 {
		return nil, "", fmt.Errorf("AWS KMS returned an empty ciphertext")
	}
	keyVersion := ""
	if result.KeyId != nil {
		keyVersion = *result.KeyId
	}
	return result.CiphertextBlob, keyVersion, nil
}

func (k *awsKMS) Unwrap(ctx context.Context, ciphertext []byte, identity map[string]string, keyRef, keyVersion string) ([]byte, string, error) {
	decryptKey := keyVersion
	if decryptKey == "" {
		decryptKey = keyRef
	}
	if decryptKey == "" {
		return nil, "", fmt.Errorf("credential envelope is missing the AWS KMS key reference")
	}
	result, err := k.client.Decrypt(ctx, &kms.DecryptInput{
		CiphertextBlob:    ciphertext,
		EncryptionContext: kmsEncryptionContext(identity),
		KeyId:             aws.String(decryptKey),
	})
	if err != nil {
		return nil, "", fmt.Errorf("AWS KMS decrypt request failed")
	}
	if len(result.Plaintext) == 0 {
		return nil, "", fmt.Errorf("AWS KMS returned an empty plaintext")
	}
	version := ""
	if result.KeyId != nil {
		version = *result.KeyId
	}
	return result.Plaintext, version, nil
}
