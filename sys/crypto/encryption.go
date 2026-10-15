package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	atomicwriter "go.getarcane.app/acfs/atomic"
)

const (
	// KeySize is the AES-256 key size used by Encryptor.
	KeySize = 32

	keyDerivationContext = "go.getarcane.app/sys/crypto aes-gcm key"
)

var (
	randReader       = rand.Reader
	defaultEncryptor atomic.Pointer[Encryptor]
)

// Config contains the key material used to initialize the package default
// encryptor for applications that have not yet threaded an Encryptor through
// every callsite.
type Config struct {
	EncryptionKey string
	Environment   string
	AgentMode     bool
}

// InitEncryption initializes the package default encryptor. Applications should
// prefer constructing an Encryptor with New and passing it explicitly.
func InitEncryption(cfg *Config) {
	if cfg == nil {
		panic("encryption config is required")
	}
	keyStr := strings.TrimSpace(cfg.EncryptionKey)
	if keyStr == "" {
		if cfg.Environment == "production" && !cfg.AgentMode {
			panic("ENCRYPTION_KEY is required in production environment")
		}
		keyStr = "arcane-dev-key"
	}
	key, err := ParseKey(keyStr)
	if err != nil {
		panic(err.Error())
	}
	var legacy [][]byte
	for _, decode := range []func(string) ([]byte, error){hex.DecodeString, base64.StdEncoding.DecodeString, base64.RawStdEncoding.DecodeString} {
		if decoded, decodeErr := decode(keyStr); decodeErr == nil && len(decoded) >= KeySize {
			legacy = append(legacy, bytes.Clone(decoded[:KeySize]))
		}
	}
	if len(keyStr) >= KeySize {
		legacy = append(legacy, []byte(keyStr[:KeySize]))
	}
	encryptor, err := New(key, legacy...)
	if err != nil {
		panic(err.Error())
	}
	defaultEncryptor.Store(encryptor)
}

// Encrypt encrypts with the package default encryptor.
func Encrypt(plaintext string) (string, error) {
	encryptor, err := Default()
	if err != nil {
		return "", err
	}
	return encryptor.Encrypt(plaintext)
}

// Decrypt decrypts with the package default encryptor.
func Decrypt(ciphertext string) (string, error) {
	encryptor, err := Default()
	if err != nil {
		return "", err
	}
	return encryptor.Decrypt(ciphertext)
}

// Default returns the package default encryptor.
func Default() (*Encryptor, error) {
	encryptor := defaultEncryptor.Load()
	if encryptor == nil {
		return nil, &InvalidKeyError{Reason: "encryption not initialized"}
	}
	return encryptor, nil
}

// Encryptor encrypts and decrypts strings with AES-GCM.
type Encryptor struct {
	keys [][]byte
}

// New creates an Encryptor with a primary AES-256 key and optional old keys for
// decryption during rotation.
func New(primary []byte, old ...[]byte) (*Encryptor, error) {
	keys := make([][]byte, 0, 1+len(old))
	primaryKey, err := copyAESKey(primary)
	if err != nil {
		return nil, err
	}
	keys = append(keys, primaryKey)
	for i, key := range old {
		normalized, keyErr := copyAESKey(key)
		if keyErr != nil {
			return nil, &InvalidKeyError{Reason: fmt.Sprintf("old key %d: %v", i, keyErr)}
		}
		keys = append(keys, normalized)
	}
	return &Encryptor{keys: keys}, nil
}

// ParseKey parses explicit key material. Hex and base64 keys must use the
// "hex:" or "base64:" prefixes; unprefixed values are treated as passphrases
// and derived with HKDF-SHA256 to avoid ambiguous hex-looking raw secrets.
func ParseKey(in string) ([]byte, error) {
	clean := strings.TrimSpace(in)
	if clean == "" {
		return nil, &InvalidKeyError{Reason: "key is empty"}
	}
	if encoded, ok := strings.CutPrefix(clean, "hex:"); ok {
		decoded, err := hex.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return nil, &InvalidKeyError{Reason: "hex key is invalid"}
		}
		return validateEncodedKey(decoded)
	}
	if encoded, ok := strings.CutPrefix(clean, "base64:"); ok {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return nil, &InvalidKeyError{Reason: "base64 key is invalid"}
		}
		return validateEncodedKey(decoded)
	}
	clean = strings.TrimPrefix(clean, "raw:")
	if clean == "" {
		return nil, &InvalidKeyError{Reason: "key material is empty"}
	}
	return hkdf.Key(sha256.New, []byte(clean), nil, keyDerivationContext, KeySize)
}

// GenerateKey creates a random AES-256 key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(randReader, key); err != nil {
		return nil, &RandomError{Op: "generate encryption key", Err: err}
	}
	return key, nil
}

// LoadKeyFile reads a hex-encoded key file.
func LoadKeyFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, &InvalidKeyError{Reason: "key file is not valid hex"}
	}
	return copyAESKey(decoded)
}

// SaveKeyFile atomically writes a key as hex with 0600 permissions.
func SaveKeyFile(path string, key []byte) error {
	normalized, err := copyAESKey(key)
	if err != nil {
		return err
	}
	return atomicwriter.WriteFile(path, []byte(hex.EncodeToString(normalized)+"\n"), 0o600)
}

// Encrypt encrypts a plaintext string. The empty string is returned unchanged
// so existing nullable stored-credential behavior remains explicit. Each call
// uses a fresh random 96-bit nonce, so a key must not seal more than 2^32
// messages.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if e == nil || len(e.keys) == 0 {
		return "", &InvalidKeyError{Reason: "encryptor has no keys"}
	}
	if plaintext == "" {
		return "", nil
	}
	gcm, err := newGCM(e.keys[0])
	if err != nil {
		return "", err
	}
	//nolint:gosec // G407: NewGCMWithRandomNonce generates and prepends a fresh nonce; Seal requires a nil nonce.
	ciphertext := gcm.Seal(nil, nil, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64 encoded AES-GCM ciphertext string. Old keys are
// tried after the primary key so callers can rotate keys without data loss.
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if e == nil || len(e.keys) == 0 {
		return "", &InvalidKeyError{Reason: "encryptor has no keys"}
	}
	if ciphertext == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", &DecodeError{Err: err}
	}
	var lastErr error
	for _, key := range e.keys {
		gcm, gcmErr := newGCM(key)
		if gcmErr != nil {
			lastErr = gcmErr
			continue
		}
		if len(data) < gcm.Overhead() {
			lastErr = &CiphertextError{Reason: "too short"}
			continue
		}
		plaintext, openErr := gcm.Open(nil, nil, data, nil)
		if openErr == nil {
			return string(plaintext), nil
		}
		lastErr = openErr
	}
	return "", &DecryptError{Err: lastErr}
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, &InvalidKeyError{Reason: err.Error()}
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func validateEncodedKey(key []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, &InvalidKeyError{Reason: fmt.Sprintf("encoded key must be %d bytes, got %d", KeySize, len(key))}
	}
	return copyAESKey(key)
}

func copyAESKey(key []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, &InvalidKeyError{Reason: fmt.Sprintf("key must be %d bytes, got %d", KeySize, len(key))}
	}
	return bytes.Clone(key), nil
}
