// Package secure provides platform secret protection for API keys persisted
// in config. On Windows, secrets are encrypted with DPAPI
// (CryptProtectData/CryptUnprotectData), which binds the ciphertext to the
// current user and machine. On other platforms DPAPI is unavailable, so the
// value falls back to base64 obfuscation (not real encryption).
package secure
