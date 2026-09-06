package secure

import (
	"strings"
	"testing"
)

// TestRoundTrip covers the cross-platform contract: Encrypt followed by
// Decrypt must return the original plaintext for empty, ASCII, CJK, emoji,
// and large inputs. Empty short-circuits to empty on every platform.
func TestRoundTrip(t *testing.T) {
	cases := []string{
		"",
		"x",
		"sk-abc-123-456",
		"中文密钥🔑",
		strings.Repeat("a", 4096),
	}
	for _, plain := range cases {
		enc, err := Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt(%q) error: %v", plain, err)
		}
		if plain == "" {
			if enc != "" {
				t.Fatalf("Encrypt(\"\") = %q, want empty", enc)
			}
			continue
		}
		got, err := Decrypt(enc)
		if err != nil {
			t.Fatalf("Decrypt(%q) error: %v", enc, err)
		}
		if got != plain {
			t.Fatalf("round-trip mismatch: got %q, want %q", got, plain)
		}
	}
}

// TestEncryptNeverEqualsPlaintext asserts the ciphertext is never the raw
// plaintext itself (a regression guard against a no-op Encrypt). On Windows
// DPAPI produces real ciphertext; on other platforms it is base64 obfuscation
// — either way it must differ from the input.
func TestEncryptNeverEqualsPlaintext(t *testing.T) {
	plain := "sk-secret-123"
	enc, err := Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	if enc == "" {
		t.Fatal("expected non-empty ciphertext")
	}
	if enc == plain {
		t.Fatalf("ciphertext equals plaintext: %q", enc)
	}
}

// TestDecryptGarbageFails ensures malformed input errors instead of panicking.
// "not valid base64!!!" fails the base64 decode on every platform.
func TestDecryptGarbageFails(t *testing.T) {
	if _, err := Decrypt("not valid base64!!!"); err == nil {
		t.Fatal("expected error decoding non-base64 garbage")
	}
}

// TestDecryptEmptyIsZeroValue ensures the empty short-circuit round-trips.
func TestDecryptEmpty(t *testing.T) {
	got, err := Decrypt("")
	if err != nil {
		t.Fatalf("Decrypt(\"\") error: %v", err)
	}
	if got != "" {
		t.Fatalf("Decrypt(\"\") = %q, want empty", got)
	}
}
