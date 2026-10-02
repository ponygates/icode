package secure

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// resetKeyCache clears the memoised master key so a test can point the home
// directory at a temp dir and get a fresh key from it.
func resetKeyCache(t *testing.T) {
	t.Helper()
	keyOnce = sync.Once{}
	keyVal, keyErr = nil, nil
	t.Cleanup(func() {
		keyOnce = sync.Once{}
		keyVal, keyErr = nil, nil
	})
}

// fakeHome redirects the ~/.icode lookup into a temp directory. os.UserHomeDir
// reads HOME on unix and USERPROFILE on Windows, so both are set.
func fakeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	resetKeyCache(t)
	return dir
}

func TestSealOpenRoundTrip(t *testing.T) {
	fakeHome(t)
	for _, plain := range []string{"sk-abc123def456ghi", "中文密钥🔑", strings.Repeat("x", 4096)} {
		enc, err := Seal(plain)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		if !IsSealed(enc) {
			t.Fatalf("Seal output %q lacks the %q prefix", enc, CipherPrefix)
		}
		if strings.Contains(enc, plain) {
			t.Fatal("ciphertext contains the plaintext")
		}
		got, err := Open(enc)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got != plain {
			t.Fatalf("round trip: got %q, want %q", got, plain)
		}
	}
}

func TestSealIsRandomised(t *testing.T) {
	fakeHome(t)
	a, _ := Seal("same-secret")
	b, _ := Seal("same-secret")
	if a == b {
		t.Fatal("two seals of the same plaintext matched — nonce is being reused")
	}
}

func TestOpenRejectsForeignCiphertext(t *testing.T) {
	dir := fakeHome(t)
	enc, err := Seal("hunter2")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Replace the key file the way a reinstalled ~/.icode would; the old
	// ciphertext must fail authentication rather than return garbage.
	fresh := make([]byte, keyBytes)
	if _, err := rand.Read(fresh); err != nil {
		t.Fatalf("rand: %v", err)
	}
	keyPath := filepath.Join(dir, ".icode", keyFileName)
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(fresh)+"\n"), 0o600); err != nil {
		t.Fatalf("replace key: %v", err)
	}
	resetKeyCache(t)
	if _, err := Open(enc); err == nil {
		t.Fatal("Open accepted ciphertext from a different key")
	}
}

func TestCorruptKeyFileIsAnError(t *testing.T) {
	dir := fakeHome(t)
	keyPath := filepath.Join(dir, ".icode", keyFileName)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("not-base64-at-all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resetKeyCache(t)
	if _, err := Seal("x"); err == nil {
		t.Fatal("Seal accepted a corrupt key file")
	}
}

func TestKeyFileIsPrivate(t *testing.T) {
	dir := fakeHome(t)
	if _, err := Seal("bootstrap"); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, ".icode", keyFileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Windows does not enforce unix permission bits; only assert where they mean
	// something, otherwise the check is a lie on the dev platform.
	if os.PathSeparator == '/' && fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key file mode = %v, want 0600", fi.Mode().Perm())
	}
	if dirFi, err := os.Stat(filepath.Join(dir, ".icode")); err == nil &&
		os.PathSeparator == '/' && dirFi.Mode().Perm()&0o077 != 0 {
		t.Fatalf(".icode dir mode = %v, want 0700", dirFi.Mode().Perm())
	}
}
