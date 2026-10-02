package netsec

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBlockedRanges(t *testing.T) {
	strictOnly := []string{
		"127.0.0.1", "127.5.5.5", "::1",
		"10.1.2.3", "172.16.0.1", "192.168.4.4", "100.64.0.1", "0.0.0.0", "::",
		"fd00::1",
	}
	for _, s := range strictOnly {
		ip := net.ParseIP(s)
		if !blocked(ip, true) {
			t.Errorf("blocked(%q, strict) = false, want true", s)
		}
		if blocked(ip, false) && !IsMetadataIP(ip) {
			t.Errorf("blocked(%q, lenient) = true, want false (user-configured endpoints may be private)", s)
		}
	}

	// The metadata plane is refused even in lenient mode — nobody configures a
	// coding agent against 169.254.169.254 on purpose.
	for _, s := range []string{"169.254.169.254", "169.254.1.1", "fe80::1"} {
		ip := net.ParseIP(s)
		if !blocked(ip, false) {
			t.Errorf("blocked(%q, lenient) = false, want true", s)
		}
		if !IsMetadataIP(ip) {
			t.Errorf("IsMetadataIP(%q) = false, want true", s)
		}
	}

	for _, s := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2001:4860:4860::8888"} {
		if blocked(net.ParseIP(s), true) {
			t.Errorf("blocked(%q, strict) = true, want false", s)
		}
	}
}

func TestValidateURLSchemeAndShape(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x", "http:///no-host", "not a url"} {
		if err := ValidatePublicURL(u); err == nil {
			t.Errorf("ValidatePublicURL(%q) = nil, want error", u)
		}
	}
	// Literal IPs skip DNS, so these assertions are network-independent.
	if err := ValidatePublicURL("http://127.0.0.1:8080/admin"); err == nil {
		t.Error("loopback literal allowed in strict mode")
	}
	if err := ValidateConfiguredURL("http://127.0.0.1:11434/v1/models"); err != nil {
		t.Errorf("ValidateConfiguredURL(ollama) = %v, want allow", err)
	}
	if err := ValidateConfiguredURL("http://169.254.169.254/latest/meta-data"); err == nil {
		t.Error("metadata endpoint allowed in lenient mode")
	}
}

func TestGuardedTransportDialsLoopbackInLenientMode(t *testing.T) {
	hermeticProxy(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	client := GuardedClient(5*time.Second, false)
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("lenient guard refused a loopback test server: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "pong" {
		t.Fatalf("body = %q, want pong", string(body))
	}
}

func TestGuardedTransportBlocksLoopbackInStrictMode(t *testing.T) {
	hermeticProxy(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	client := GuardedClient(5*time.Second, true)
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("strict guard dialled a loopback address")
	}
	// The failure must come from the guard, not from a refused connection.
	if !strings.Contains(err.Error(), "blocked range") {
		t.Fatalf("err = %v, want guard rejection", err)
	}
}

// hermeticProxy pins the proxy env off so a dev machine running Clash does not
// route the loopback test server through it.
func hermeticProxy(t *testing.T) {
	t.Helper()
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
}
