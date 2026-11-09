package crypto

import "fmt"

type (
	// InvalidKeyError reports invalid encryption key material.
	InvalidKeyError struct {
		Reason string
	}

	// RandomError reports failure from the cryptographic random source.
	RandomError struct {
		Err error
		Op  string
	}

	// DecodeError reports ciphertext decoding failures.
	DecodeError struct {
		Err error
	}

	// CiphertextError reports malformed ciphertext.
	CiphertextError struct {
		Reason string
	}

	// DecryptError reports authenticated decryption failure.
	DecryptError struct {
		Err error
	}
)

func (e *InvalidKeyError) Error() string {
	if e == nil || e.Reason == "" {
		return "invalid encryption key"
	}
	return "invalid encryption key: " + e.Reason
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

func (e *CiphertextError) Error() string {
	if e == nil || e.Reason == "" {
		return "invalid ciphertext"
	}
	return "invalid ciphertext: " + e.Reason
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
