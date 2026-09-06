//go:build windows

package secure

import (
	"encoding/base64"
	"testing"
)

// TestDPAPIActuallyEncrypts verifies the Windows path really calls DPAPI:
// the ciphertext must NOT be plain base64 of the input (which would mean
// Encrypt silently degraded to obfuscation, leaving keys recoverable from
// the config file by anyone with filesystem access).
func TestDPAPIActuallyEncrypts(t *testing.T) {
	plain := "sk-windows-dpapi-test"
	enc, err := Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	if enc == base64.StdEncoding.EncodeToString([]byte(plain)) {
		t.Fatal("ciphertext is plain base64 — DPAPI did not encrypt")
	}
}

// TestDecryptWrongCiphertextFails ensures a valid-base64 but non-DPAPI blob
// (e.g. base64 of "hello") is rejected by CryptUnprotectData instead of
// returning garbage or panicking.
func TestDecryptWrongCiphertextFails(t *testing.T) {
	fake := base64.StdEncoding.EncodeToString([]byte("not a real DPAPI blob"))
	if _, err := Decrypt(fake); err == nil {
		t.Fatal("expected CryptUnprotectData to reject a non-DPAPI blob")
	}
}
