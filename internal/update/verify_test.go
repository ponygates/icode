package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testKey is an in-memory minisign keypair plus the file-format encoders the
// real `minisign` CLI produces, so these tests exercise the same byte layout
// without depending on an external binary.
type testKey struct {
	pub     ed25519.PublicKey
	priv    ed25519.PrivateKey
	keyID   [8]byte
	pubB64  string // value for MinisignPublicKey
	present bool
}

func newTestKey(t *testing.T) *testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	k := &testKey{pub: pub, priv: priv, present: true}
	if _, err := rand.Read(k.keyID[:]); err != nil {
		t.Fatalf("key id: %v", err)
	}
	payload := append([]byte(minisignAlgEd), k.keyID[:]...)
	payload = append(payload, pub...)
	k.pubB64 = base64.StdEncoding.EncodeToString(payload)
	return k
}

// sign returns a minisign "Ed" signature file for raw: a detached Ed25519
// signature over the raw bytes, then the global signature over raw+comment.
func (k *testKey) sign(t *testing.T, raw []byte) []byte {
	t.Helper()
	untrusted := "untrusted comment: iCode release signing key"
	main := ed25519.Sign(k.priv, raw)
	blob := append([]byte(minisignAlgEd), k.keyID[:]...)
	blob = append(blob, main...)
	globalMsg := append([]byte(nil), raw...)
	globalMsg = append(globalMsg, []byte(untrusted)...)
	global := ed25519.Sign(k.priv, globalMsg)
	return []byte(fmt.Sprintf("%s\n%s\ntrusted comment: timestamp 1730000000\n%s\n",
		untrusted,
		base64.StdEncoding.EncodeToString(blob),
		base64.StdEncoding.EncodeToString(global)))
}

// withKey points the package at a configured (or placeholder) public key and
// restores both globals after the test.
func withKey(t *testing.T, pubB64 string, env map[string]string) {
	t.Helper()
	oldKey := MinisignPublicKey
	oldEnv := osGetenv
	t.Cleanup(func() {
		MinisignPublicKey = oldKey
		osGetenv = oldEnv
	})
	MinisignPublicKey = pubB64
	osGetenv = func(k string) string { return env[k] }
}

// checksumLineFor builds a checksums.txt body for payload under name.
func checksumLineFor(name string, payload []byte) string {
	return fmt.Sprintf("%s  %s\n%s  %s.minisig\n", sha256Hex(payload), name, sha256Hex([]byte("sigfile")), name)
}

func TestValidSignatureAndChecksumAccepted(t *testing.T) {
	k := newTestKey(t)
	withKey(t, k.pubB64, nil)
	payload := []byte("MZ\x90\x00 the quick brown fake binary")
	sig := k.sign(t, payload)
	err := verifyReleaseAsset(payload, "icode-cli-windows-amd64.exe",
		checksumLineFor("icode-cli-windows-amd64.exe", payload), sig, &strings.Builder{})
	if err != nil {
		t.Fatalf("valid asset rejected: %v", err)
	}
}

func TestChecksumMismatchRejected(t *testing.T) {
	k := newTestKey(t)
	withKey(t, k.pubB64, nil)
	payload := []byte("genuine bytes")
	sig := k.sign(t, payload)
	var log strings.Builder
	err := verifyReleaseAsset(payload, "icode-cli-linux-amd64",
		checksumLineFor("icode-cli-linux-amd64", []byte("different content")), sig, &log)
	if err == nil || !strings.Contains(err.Error(), "sha256 校验失败") {
		t.Fatalf("checksum mismatch accepted: %v", err)
	}
}

func TestSignatureOverTamperedBytesRejected(t *testing.T) {
	k := newTestKey(t)
	withKey(t, k.pubB64, nil)
	original := []byte("signed payload")
	sig := k.sign(t, original)
	tampered := []byte("signed payloaX")
	// checksums.txt is generated over the tampered file (attacker who controls
	// the CDN can forge the hash) — the signature must still stop it.
	err := verifyReleaseAsset(tampered, "icode-cli-darwin-arm64",
		checksumLineFor("icode-cli-darwin-arm64", tampered), sig, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "签名校验失败") {
		t.Fatalf("tampered payload accepted: %v", err)
	}
}

func TestSignatureFromOtherKeyRejected(t *testing.T) {
	k := newTestKey(t)
	other := newTestKey(t)
	withKey(t, k.pubB64, nil)
	payload := []byte("payload")
	err := verifyReleaseAsset(payload, "icode-cli-linux-arm64",
		checksumLineFor("icode-cli-linux-arm64", payload), other.sign(t, payload), &strings.Builder{})
	if err == nil {
		t.Fatal("signature from an unrelated key accepted")
	}
	if !strings.Contains(err.Error(), "key id 不匹配") {
		t.Fatalf("unexpected error (want key id mismatch): %v", err)
	}
}

func TestMissingSignatureRejectedWhenKeyConfigured(t *testing.T) {
	k := newTestKey(t)
	withKey(t, k.pubB64, nil)
	payload := []byte("payload")
	err := verifyReleaseAsset(payload, "icode-cli-freebsd-amd64",
		checksumLineFor("icode-cli-freebsd-amd64", payload), nil, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "minisig") {
		t.Fatalf("unsigned asset accepted while a key is configured: %v", err)
	}
}

func TestMissingChecksumEntryRejected(t *testing.T) {
	k := newTestKey(t)
	withKey(t, k.pubB64, nil)
	payload := []byte("payload")
	err := verifyReleaseAsset(payload, "icode-cli-windows-arm64.exe",
		"deadbeef  something-else.bin\n", k.sign(t, payload), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "sha256 条目") {
		t.Fatalf("asset absent from checksums.txt accepted: %v", err)
	}
}

// Placeholder-key path: no key baked in ⇒ fail closed, and only the explicit
// env escape hatch relaxes it — with a loud warning.
func TestPlaceholderKeyFailsClosed(t *testing.T) {
	withKey(t, minisignPublicKeyPlaceholder, nil)
	if PublicKeyConfigured() {
		t.Fatal("placeholder key reported as configured")
	}
	payload := []byte("payload")
	var log strings.Builder
	err := verifyReleaseAsset(payload, "icode-cli-linux-amd64",
		checksumLineFor("icode-cli-linux-amd64", payload), nil, &log)
	if !strings.Contains(err.Error(), "未内置 minisign 公钥") {
		t.Fatalf("placeholder key did not fail closed: %v", err)
	}
	if log.Len() != 0 {
		t.Fatalf("nothing should be installed or warned yet, got: %s", log.String())
	}
}

func TestPlaceholderKeyEscapeHatchWarns(t *testing.T) {
	withKey(t, minisignPublicKeyPlaceholder, map[string]string{AllowUnsignedEnv: "1"})
	payload := []byte("payload")
	var log strings.Builder
	err := verifyReleaseAsset(payload, "icode-cli-linux-amd64",
		checksumLineFor("icode-cli-linux-amd64", payload), nil, &log)
	if err != nil {
		t.Fatalf("escape hatch path errored: %v", err)
	}
	out := log.String()
	if !strings.Contains(out, "SECURITY WARNING") || !strings.Contains(out, AllowUnsignedEnv) {
		t.Fatalf("escape hatch did not print a loud warning: %q", out)
	}
}

// The escape hatch must NOT weaken a build that has a real key.
func TestEscapeHatchIgnoredWhenKeyConfigured(t *testing.T) {
	k := newTestKey(t)
	withKey(t, k.pubB64, map[string]string{AllowUnsignedEnv: "1"})
	payload := []byte("payload")
	err := verifyReleaseAsset(payload, "icode-cli-linux-amd64",
		checksumLineFor("icode-cli-linux-amd64", payload), nil, &strings.Builder{})
	if err == nil {
		t.Fatal("ICODE_UPDATE_ALLOW_UNSIGNED bypassed signature verification")
	}
}

func TestVerifyDownloadedRefusesReleaseWithoutChecksums(t *testing.T) {
	k := newTestKey(t)
	withKey(t, k.pubB64, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // no checksums.txt in the release at all
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := filepath.Join(dir, "icode-cli-linux-amd64")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := verifyDownloaded(context.Background(), path, "icode-cli-linux-amd64",
		map[string]string{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), checksumsAsset) {
		t.Fatalf("release without checksums.txt was accepted: %v", err)
	}
}

func TestVerifyDownloadedEndToEnd(t *testing.T) {
	k := newTestKey(t)
	withKey(t, k.pubB64, nil)
	const asset = "icode-cli-linux-amd64"
	payload := []byte("fake but consistent binary")
	sig := k.sign(t, payload)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+checksumsAsset, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, checksumLineFor(asset, payload))
	})
	mux.HandleFunc("/"+asset+".minisig", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(sig)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := filepath.Join(dir, asset)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	urls := map[string]string{
		checksumsAsset:     srv.URL + "/" + checksumsAsset,
		asset + ".minisig": srv.URL + "/" + asset + ".minisig",
	}
	if err := verifyDownloaded(context.Background(), path, asset, urls, &strings.Builder{}); err != nil {
		t.Fatalf("good release rejected: %v", err)
	}

	// Attacker swaps the binary but keeps the (now stale) checksums entry.
	bad := append([]byte(nil), payload...)
	bad[0] = 'X'
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyDownloaded(context.Background(), path, asset, urls, &strings.Builder{}); err == nil {
		t.Fatal("swapped binary accepted")
	}
}

func TestEmbeddedPublicKeyValidation(t *testing.T) {
	withKey(t, minisignPublicKeyPlaceholder, nil)
	if _, _, err := embeddedPublicKey(); err == nil {
		t.Fatal("placeholder key parsed as valid")
	}
	k := newTestKey(t)
	pub, keyID, err := embeddedPublicKeyFor(k.pubB64)
	if err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize || string(keyID) != string(k.keyID[:]) {
		t.Fatalf("decoded key mismatch: pub=%d bytes keyID=%x", len(pub), keyID)
	}
	// Truncated payload must be refused, not silently padded.
	short := base64.StdEncoding.EncodeToString([]byte("Edshort"))
	if _, _, err := embeddedPublicKeyFor(short); err == nil {
		t.Fatal("truncated public key accepted")
	}
	// Pre-hashed ("ED") keys are out of scope for a stdlib-only verifier.
	hashed := base64.StdEncoding.EncodeToString(append(append([]byte("ED"), k.keyID[:]...), k.pub...))
	if _, _, err := embeddedPublicKeyFor(hashed); err == nil {
		t.Fatal("pre-hashed ED public key accepted")
	}
}

func TestParseChecksumsFormats(t *testing.T) {
	body := "" +
		"# comment line ignored\n" +
		"aaaabbbbccccddddeeeeffff0000111122223333444455556666777788889999  icode-cli-linux-amd64\n" +
		"aaaabbbbccccddddeeeeffff000011112222333344445555666677778888AAAA  *icode-cli-windows-amd64.exe\n" +
		"shortdigest  bogus\n" +
		"\n"
	got := parseChecksums(body)
	if len(got) != 2 {
		t.Fatalf("parsed %d entries, want 2: %v", len(got), got)
	}
	if got["icode-cli-linux-amd64"] != "aaaabbbbccccddddeeeeffff0000111122223333444455556666777788889999" {
		t.Fatalf("bad entry: %v", got)
	}
	if got["icode-cli-windows-amd64.exe"] != "aaaabbbbccccddddeeeeffff000011112222333344445555666677778888aaaa" {
		t.Fatalf("binary-mode/star-prefixed name not handled: %v", got)
	}
}

func TestVerifyMinisigRejectsGarbageSignatureFile(t *testing.T) {
	k := newTestKey(t)
	withKey(t, k.pubB64, nil)
	payload := []byte("payload")
	cases := map[string][]byte{
		"empty":           nil,
		"not base64":      []byte("untrusted comment: x\n!!!not-base64!!!\n"),
		"too short":       []byte("untrusted comment: x\n" + base64.StdEncoding.EncodeToString([]byte("Edtiny")) + "\n"),
		"comments only":   []byte("untrusted comment: x\ntrusted comment: y\n"),
		"wrong algorithm": []byte("untrusted comment: x\n" + base64.StdEncoding.EncodeToString(append(append([]byte("XX"), k.keyID[:]...), make([]byte, 64)...)) + "\n"),
	}
	for name, sig := range cases {
		if err := verifyMinisig(payload, sig); err == nil {
			t.Errorf("%s: garbage signature accepted", name)
		}
	}
}
