package crypto

import "fmt"

// InvalidKeyError reports invalid encryption key material.
type InvalidKeyError struct {
	Reason string
}

func (e *InvalidKeyError) Error() string {
	if e == nil || e.Reason == "" {
		return "invalid encryption key"
	}
	return "invalid encryption key: " + e.Reason
}

// RandomError reports failure from the cryptographic random source.
type RandomError struct {
	Op  string
	Err error
}

func (e *RandomError) Error() string {
	if e == nil {
		return "cryptographic random failure"
	}
	if e.Op == "" {
		return fmt.Sprintf("cryptographic random failure: %v", e.Err)
	}
	return fmt.Sprintf("%s: cryptographic random failure: %v", e.Op, e.Err)
}

func (e *RandomError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// DecodeError reports ciphertext decoding failures.
type DecodeError struct {
	Err error
}

func (e *DecodeError) Error() string {
	if e == nil {
		return "failed to decode ciphertext"
	}
	return fmt.Sprintf("failed to decode ciphertext: %v", e.Err)
}

func (e *DecodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// CiphertextError reports malformed ciphertext.
type CiphertextError struct {
	Reason string
}

func (e *CiphertextError) Error() string {
	if e == nil || e.Reason == "" {
		return "invalid ciphertext"
	}
	return "invalid ciphertext: " + e.Reason
}

// DecryptError reports authenticated decryption failure.
type DecryptError struct {
	Err error
}

func (e *DecryptError) Error() string {
	if e == nil {
		return "failed to decrypt"
	}
	return fmt.Sprintf("failed to decrypt: %v", e.Err)
}

func (e *DecryptError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
