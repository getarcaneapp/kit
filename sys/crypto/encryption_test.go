package crypto

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseKeyDerivesRawPassphrasesWithoutTruncating(t *testing.T) {
	key, err := ParseKey("this-is-a-raw-passphrase-that-is-long-enough")
	if err != nil {
		t.Fatalf("ParseKey returned error: %v", err)
	}
	if len(key) != KeySize {
		t.Fatalf("ParseKey returned %d bytes, want %d", len(key), KeySize)
	}
	if bytes.Equal(key, []byte("this-is-a-raw-passphrase-that-is")) {
		t.Fatal("ParseKey truncated the raw passphrase instead of deriving a key")
	}
}

func TestParseKeyPrefersRawForHexLookalikePassphrase(t *testing.T) {
	hexLookalike := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	key, err := ParseKey(hexLookalike)
	if err != nil {
		t.Fatalf("ParseKey returned error: %v", err)
	}
	decoded, err := hex.DecodeString(hexLookalike)
	if err != nil {
		t.Fatalf("DecodeString returned error: %v", err)
	}
	if bytes.Equal(key, decoded) {
		t.Fatal("ParseKey decoded a hex-looking raw passphrase instead of deriving it")
	}
}

func TestInitEncryptionDecryptsLegacyHexCiphertext(t *testing.T) {
	hexLookalike := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	legacyKey, err := hex.DecodeString(hexLookalike)
	if err != nil {
		t.Fatalf("DecodeString returned error: %v", err)
	}
	legacyEncryptor, err := New(legacyKey)
	if err != nil {
		t.Fatalf("New legacy returned error: %v", err)
	}
	ciphertext, err := legacyEncryptor.Encrypt("legacy secret")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	InitEncryption(&Config{EncryptionKey: hexLookalike, Environment: "production"})
	plaintext, err := Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if plaintext != "legacy secret" {
		t.Fatalf("Decrypt = %q, want legacy secret", plaintext)
	}
}

func TestParseEncodedKey(t *testing.T) {
	raw := bytes.Repeat([]byte{0x42}, KeySize)
	for _, tc := range []struct {
		name  string
		input string
	}{
		{name: "hex prefixed", input: "hex:" + hex.EncodeToString(raw)},
		{name: "base64 prefixed", input: "base64:" + base64.StdEncoding.EncodeToString(raw)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, err := ParseKey(tc.input)
			if err != nil {
				t.Fatalf("ParseKey returned error: %v", err)
			}
			if !bytes.Equal(key, raw) {
				t.Fatalf("ParseKey = %x, want %x", key, raw)
			}
		})
	}
}

func TestParseKeyRejectsOverLengthEncodedKey(t *testing.T) {
	_, err := ParseKey("hex:" + hex.EncodeToString(bytes.Repeat([]byte{0x42}, KeySize+1)))
	if err == nil {
		t.Fatal("ParseKey returned nil error")
	}
	var invalid *InvalidKeyError
	if !errors.As(err, &invalid) {
		t.Fatalf("ParseKey error type = %T, want *InvalidKeyError", err)
	}
}

func TestEncryptorEncryptDecrypt(t *testing.T) {
	key, err := ParseKey("primary-key-material")
	if err != nil {
		t.Fatalf("ParseKey returned error: %v", err)
	}
	encryptor, err := New(key)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	ciphertext, err := encryptor.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	if ciphertext == "" || ciphertext == "secret" {
		t.Fatalf("Encrypt returned invalid ciphertext %q", ciphertext)
	}
	plaintext, err := encryptor.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if plaintext != "secret" {
		t.Fatalf("Decrypt = %q, want secret", plaintext)
	}
}

func TestEncryptorDecryptsWithOldKey(t *testing.T) {
	oldKey, err := ParseKey("old-key-material")
	if err != nil {
		t.Fatalf("ParseKey old returned error: %v", err)
	}
	oldEncryptor, err := New(oldKey)
	if err != nil {
		t.Fatalf("New old returned error: %v", err)
	}
	ciphertext, err := oldEncryptor.Encrypt("rotated secret")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	newKey, err := ParseKey("new-key-material")
	if err != nil {
		t.Fatalf("ParseKey new returned error: %v", err)
	}
	rotated, err := New(newKey, oldKey)
	if err != nil {
		t.Fatalf("New rotated returned error: %v", err)
	}
	plaintext, err := rotated.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if plaintext != "rotated secret" {
		t.Fatalf("Decrypt = %q, want rotated secret", plaintext)
	}
}

func TestEncryptorEncryptReturnsRandomFailure(t *testing.T) {
	key, err := ParseKey("primary-key-material")
	if err != nil {
		t.Fatalf("ParseKey returned error: %v", err)
	}
	encryptor, err := New(key)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	oldReader := randReader
	randReader = errReader{}
	t.Cleanup(func() { randReader = oldReader })

	_, err = encryptor.Encrypt("secret")
	if err == nil {
		t.Fatal("Encrypt returned nil error")
	}
	var randomErr *RandomError
	if !errors.As(err, &randomErr) {
		t.Fatalf("Encrypt error type = %T, want *RandomError", err)
	}
}

func TestGenerateKeyReturnsRandomFailure(t *testing.T) {
	oldReader := randReader
	randReader = errReader{}
	t.Cleanup(func() { randReader = oldReader })

	_, err := GenerateKey()
	if err == nil {
		t.Fatal("GenerateKey returned nil error")
	}
	var randomErr *RandomError
	if !errors.As(err, &randomErr) {
		t.Fatalf("GenerateKey error type = %T, want *RandomError", err)
	}
}

func TestKeyFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	key := bytes.Repeat([]byte{0x42}, KeySize)
	if err := SaveKeyFile(path, key); err != nil {
		t.Fatalf("SaveKeyFile returned error: %v", err)
	}
	loaded, err := LoadKeyFile(path)
	if err != nil {
		t.Fatalf("LoadKeyFile returned error: %v", err)
	}
	if !bytes.Equal(loaded, key) {
		t.Fatalf("LoadKeyFile = %x, want %x", loaded, key)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %v, want 0600", got)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("random unavailable")
}
