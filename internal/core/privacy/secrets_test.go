package privacy

import (
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactSecrets_KnownCredentialShapes(t *testing.T) {
	cases := map[string]string{
		"key sk-ant-apieceofsecrettext here":      "sk-ant-",
		"using sk-abcdefghijklmnopqrstuvwx now":   "sk-abc",
		"AWSID AKIAIOSFODNN7EXAMPLE":              "AKIA",
		"gh token ghp_abcdefghijklmnopqrstuvwxyz": "ghp_",
		// Split literals so secret scanners do not flag the fixture itself.
		"slack xox" + "b-1234567890-abcdefghijklmnop":     "xoxb",
		"google AIzaSyabcdefghijklmnopqrstuvwxyzABCDEFG1": "AIzaSy",
		"Authorization: Bearer abcdef123456ghi":           "Bearer",
		"api_key = 'supersecretvalue123'":                 "api_key",
		"password:pa55word-for-the-service":               "password",
	}
	for in, wantKept := range cases {
		got := RedactSecrets(in)
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("RedactSecrets(%q) = %q, want a redaction marker", in, got)
		}
		// The identifying shape survives; the secret body does not.
		if wantKept != "" && !strings.Contains(got, wantKept) {
			t.Errorf("RedactSecrets(%q) = %q, lost the %q fingerprint", in, got, wantKept)
		}
	}
}

func TestRedactSecrets_LeavesOrdinaryTextAlone(t *testing.T) {
	// A redactor that eats code is worse than none: these must survive so logs
	// stay readable and diffs still make sense.
	safe := []string{
		"func main() { fmt.Println(\"hello\") }",
		"GET /api/v1/models 200 12ms",
		"internal/core/tool/tools.go:1438: undefined: net",
		"the sk short token in prose stays",
		"sk-abc",                      // far too short to be a real key
		"error: no such host foo.com", // network text, no credential
	}
	for _, s := range safe {
		if got := RedactSecrets(s); got != s {
			t.Errorf("RedactSecrets(%q) = %q, want unchanged", s, got)
		}
	}
}

func TestRedactSecrets_PrivateKeyBlock(t *testing.T) {
	pem := "noise\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----\ntail"
	got := RedactSecrets(pem)
	if strings.Contains(got, "MIIEowIBAAKCAQEA") {
		t.Fatalf("private key material survived:\n%s", got)
	}
}

// TestRedactSecrets_CredentialShapes2026 pins the vendor formats the first
// pass missed (AWS STS, webhook URLs, project-scoped OpenAI keys, PEM blocks
// truncated by a crash before their END line).
func TestRedactSecrets_CredentialShapes2026(t *testing.T) {
	cases := map[string]string{
		"sts key ASIAIOSFODNN7EXAMPLE1":                     "ASIA",
		"openai proj sk-proj-abcdefghijklmnopqrstuvwxyzABC": "sk-proj-",
		// Webhook fixtures are split for the same scanner-avoidance reason.
		"hook https://hooks.slack.com/services/T00001/" + "B00002/abcdefghijklmno":    "hooks.slack.com/services/",
		"hook https://discord.com/api/webhooks/123456789012345678/AbCdEfGhIjKlMnOp":   "discord.com/api/webhooks/",
		"gh fine grain github_pat_11ABCDEFGH_abcdefghijklmnopqrstuvwx":                "github_pat_",
		"truncated pem\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAA": "",
	}
	for in, wantKept := range cases {
		got := RedactSecrets(in)
		if strings.Contains(got, in) {
			t.Errorf("RedactSecrets(%q) unchanged, want redaction", in)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("RedactSecrets(%q) = %q, want a redaction marker", in, got)
		}
		if wantKept != "" && !strings.Contains(got, wantKept) {
			t.Errorf("RedactSecrets(%q) = %q, lost the %q fingerprint", in, got, wantKept)
		}
		// Truncated PEM: the key body itself must be gone, not just marked up.
		if strings.Contains(in, "b3BlbnNzaC1rZXktdjEAAAA") && strings.Contains(got, "b3BlbnNzaC1rZXktdjEAAAA") {
			t.Errorf("truncated PEM body survived: %q", got)
		}
	}
}

func TestRedactSecrets_NoFalsePositives(t *testing.T) {
	// Identifier shapes that contain "sk-"/"gh" mid-word must not be eaten:
	// task/disk/risk IDs are everywhere in agent logs.
	safe := []string{
		"task-01HZY3K8N7M2Q4R6S8T0V2X4Z6 completed",
		"disk-abcdef0123456789abcdef mounted",
		"risk-level-abcdef012345678 elevated",
		"sk := []string{\"a\", \"b\"}", // Go variable named sk
		"// sk-index maps tenants to shards",
		"func TestSlugGeneration(t *testing.T) { slug := makeSlug(\"sk-\") }",
		"config path: C:\\Users\\ming\\dev\\app.yaml",
		"GET /v1/chat/completions 429 3ms",
	}
	for _, s := range safe {
		if got := RedactSecrets(s); got != s {
			t.Errorf("RedactSecrets(%q) = %q, want unchanged", s, got)
		}
	}
}

func TestRedactSecrets_Base64BlobSurvives(t *testing.T) {
	// Large base64 payloads (images, attachments) appear in tool output logs;
	// only genuine credential shapes may be masked.
	blob := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("iCode payload ", 512)))
	in := "tool result attachment: " + blob
	if got := RedactSecrets(in); got != in {
		t.Fatalf("base64 blob was altered (len %d -> %d)", len(in), len(got))
	}
}

func TestRedactSecrets_Idempotent(t *testing.T) {
	// Re-running the redactor over its own output must not corrupt the
	// [REDACTED] markers or surrounding prose (log lines get filtered twice:
	// once by the writer, once by the /bug bundle).
	inputs := []string{
		"Authorization: Bearer sk-abcdefghijklmnopqrstuvwx",
		"api_key = 'supersecretvalue123'",
		"password:pa55word-for-the-service",
		"key sk-ant-apieceofsecrettext here",
		"hook https://hooks.slack.com/services/T00001/" + "B00002/abcdefghijklmno",
	}
	for _, in := range inputs {
		once := RedactSecrets(in)
		if twice := RedactSecrets(once); twice != once {
			t.Errorf("not idempotent:\n  once  = %q\n  twice = %q", once, twice)
		}
	}
}

func TestRedactingWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := NewRedactingWriter(f)
	if _, err := io.WriteString(w, "configured key sk-abcdefghijklmnopqrstuvwx\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "sk-abcdefghijklmnopqrstuvwx") {
		t.Fatalf("secret reached disk: %s", raw)
	}
	if !strings.Contains(string(raw), "configured key") {
		t.Fatalf("surrounding text was eaten: %s", raw)
	}
}
