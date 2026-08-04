//go:build !windows

package secure

import "encoding/base64"

// Encrypt obfuscates plain on non-Windows platforms where DPAPI is not
// available. The value is base64-encoded, NOT cryptographically protected —
// use a real secret store on those platforms for shared machines.
func Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	return base64.StdEncoding.EncodeToString([]byte(plain)), nil
}

// Decrypt reverses Encrypt.
func Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
