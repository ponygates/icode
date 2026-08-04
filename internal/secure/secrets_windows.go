//go:build windows

package secure

import (
	"encoding/base64"
	"syscall"
	"unsafe"
)

// CRYPTPROTECT_UI_FORBIDDEN — never show a password prompt during protect.
const cryptprotectUIForbidden = 0x01

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree          = kernel32.NewProc("LocalFree")
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

// Encrypt protects plain with DPAPI scoped to the current user and machine,
// returning base64-encoded ciphertext. An empty input yields an empty output.
func Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	in := []byte(plain)
	inBlob := &dataBlob{pbData: &in[0], cbData: uint32(len(in))}
	var out dataBlob
	r1, _, e1 := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(inBlob)), 0, 0, 0, 0, cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r1 == 0 {
		return "", e1
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	outBytes := unsafe.Slice(out.pbData, out.cbData)
	cipher := make([]byte, len(outBytes))
	copy(cipher, outBytes)
	return base64.StdEncoding.EncodeToString(cipher), nil
}

// Decrypt reverses Encrypt. Fails if the ciphertext was produced by a
// different user or machine (DPAPI key no longer matches).
func Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	inBlob := &dataBlob{pbData: &raw[0], cbData: uint32(len(raw))}
	var out dataBlob
	r1, _, e1 := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(inBlob)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	if r1 == 0 {
		return "", e1
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	outBytes := unsafe.Slice(out.pbData, out.cbData)
	plain := make([]byte, len(outBytes))
	copy(plain, outBytes)
	return string(plain), nil
}
