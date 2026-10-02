//go:build !windows

package secure

import "encoding/base64"

// Encrypt protects plain at rest with AES-256-GCM under an installation key
// in ~/.icode/.master.key (see masterkey.go). Values written by older builds
// were base64 only; they still decrypt, and are re-sealed on the next save.
func Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	return Seal(plain)
}

// Decrypt reverses Encrypt, transparently handling the legacy base64 form.
func Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	if IsSealed(encoded) {
		return Open(encoded)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
