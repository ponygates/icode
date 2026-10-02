// Package secure provides platform secret protection for API keys persisted
// in config. On Windows, secrets are encrypted with DPAPI
// (CryptProtectData/CryptUnprotectData), which binds the ciphertext to the
// current user and machine. On other platforms DPAPI is unavailable, so
// secrets are sealed with AES-256-GCM under an installation key held in a 0600
// file (~/.icode/.master.key). Values written by older builds as bare base64
// still decrypt and are re-sealed on the next save.
package secure
