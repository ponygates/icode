package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Master-key file for the non-DPAPI platforms. Windows binds secrets to the
// logon user with DPAPI; there is no equivalent syscall on Linux/macOS, so at
// rest protection comes from an AES-256-GCM key kept in a 0600 file next to
// the config. That is a real barrier against the failure modes that actually
// hurt: a config.yaml copied into a backup, dotfiles synced to a repo, a
// screenshot of the settings page. It is not a defence against someone who can
// already read the user's home directory.
const (
	keyFileName = ".master.key"
	// CipherPrefix marks a value sealed with the AES-256-GCM form below.
	CipherPrefix = "gcm1:"
	keyBytes     = 32
)

var (
	keyOnce sync.Once
	keyVal  []byte
	keyErr  error
)

// masterKey returns the installation key, creating it on first use. The key is
// read with the same 0600 the writer used; a group/world-readable key file is
// rejected rather than silently trusted.
func masterKey() ([]byte, error) {
	keyOnce.Do(func() {
		keyVal, keyErr = loadOrCreateKey()
	})
	if keyErr != nil {
		return nil, keyErr
	}
	return keyVal, nil
}

func icodeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("secure: no user home directory")
	}
	dir := filepath.Join(home, ".icode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("secure: mkdir %s: %w", dir, err)
	}
	return dir, nil
}

func loadOrCreateKey() ([]byte, error) {
	dir, err := icodeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, keyFileName)
	if raw, err := os.ReadFile(path); err == nil {
		key, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if decErr != nil || len(key) != keyBytes {
			return nil, fmt.Errorf("secure: %s is corrupt — delete it to generate a new key (existing encrypted secrets would then need re-entry)", path)
		}
		if fi, statErr := os.Stat(path); statErr == nil && fi.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("secure: %s is mode %v, want 0600", path, fi.Mode().Perm())
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("secure: read %s: %w", path, err)
	}

	key := make([]byte, keyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("secure: generate key: %w", err)
	}
	// O_EXCL so two processes racing on first run cannot each write a
	// different key and leave the loser's ciphertext undecryptable.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("secure: %s appeared concurrently — restart icode", path)
		}
		return nil, fmt.Errorf("secure: create %s: %w", path, err)
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n"); err != nil {
		f.Close()
		return nil, fmt.Errorf("secure: write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("secure: close %s: %w", path, err)
	}
	return key, nil
}

// seal encrypts plain with AES-256-GCM under the master key. The result is
// prefixed so Decrypt can tell it apart from the legacy base64 form.
// Seal encrypts plain with AES-256-GCM under the installation key.
func Seal(plain string) (string, error) {
	key, err := masterKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("secure: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("secure: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secure: nonce: %w", err)
	}
	out := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return CipherPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// Open reverses Seal. A wrong or replaced key surfaces as an authentication
// failure, never as garbage plaintext.
func Open(encoded string) (string, error) {
	key, err := masterKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, CipherPrefix))
	if err != nil {
		return "", fmt.Errorf("secure: ciphertext base64: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("secure: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("secure: gcm: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("secure: ciphertext truncated")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("secure: decrypt failed — %s does not match this secret: %w", keyFileName, err)
	}
	return string(plain), nil
}

// IsSealed reports whether an stored value is in the AES-256-GCM form.
func IsSealed(encoded string) bool { return strings.HasPrefix(encoded, CipherPrefix) }
