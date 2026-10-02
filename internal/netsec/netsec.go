// Package netsec centralises SSRF validation so every outbound dial site
// (model-supplied fetch, vendor /models pulls, MCP transports, doc scrapers)
// shares one policy instead of each rolling its own — or none.
//
// Two tiers, because the trust level of the URL differs:
//
//   - ValidatePublicURL: the URL came from the model / an untrusted document.
//     Loopback, private, link-local and CGNAT ranges are all refused.
//   - ValidateConfiguredURL: the URL was typed by the user in config.yaml
//     (Ollama on 127.0.0.1, an internal gateway). Those are legitimate, so only
//     the link-local/cloud-metadata ranges are refused — they are never a
//     deliberate coding-agent endpoint, and a stored SSRF there leaks cloud
//     credentials on every later run.
package netsec

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

// ValidatePublicURL refuses anything that is not a publicly routed http(s)
// endpoint. Every resolved IP is checked, so a hostname mixing public and
// private records cannot slip through.
func ValidatePublicURL(raw string) error {
	return validate(raw, true)
}

// ValidateConfiguredURL guards URLs the user placed in config themselves.
// Loopback/private stay allowed; only link-local/metadata is blocked.
func ValidateConfiguredURL(raw string) error {
	return validate(raw, false)
}

func validate(raw string, strict bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("netsec: invalid URL %q", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("netsec: only http/https URLs are allowed (got %q)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("netsec: missing host in %q", raw)
	}
	// A literal IP needs no lookup, and skipping it avoids a pointless DNS round.
	if ip := net.ParseIP(host); ip != nil {
		if blocked(ip, strict) {
			return blockedErr(ip, strict)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("netsec: cannot resolve host %q", host)
	}
	if len(ips) == 0 {
		return fmt.Errorf("netsec: no addresses for host %q", host)
	}
	for _, ip := range ips {
		if blocked(ip, strict) {
			return blockedErr(ip, strict)
		}
	}
	return nil
}

func blockedErr(ip net.IP, strict bool) error {
	kind := "link-local/metadata"
	if strict {
		kind = "private/loopback/link-local"
	}
	return fmt.Errorf("netsec: blocked address %s (%s not allowed)", ip, kind)
}

func blocked(ip net.IP, strict bool) bool {
	if ip == nil {
		return true
	}
	// Blocked for everyone: the cloud metadata plane is never a legitimate
	// endpoint, even when the user typed it.
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 169 && v4[1] == 254 {
			return true // 169.254.0.0/16 — includes 169.254.169.254
		}
	} else if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true // fe80::/10, ff02::/16
	}
	if !strict {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 127: // loopback
			return true
		case v4[0] == 0: // 0.0.0.0/8
			return true
		case v4[0] == 10: // 10.0.0.0/8
			return true
		case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31: // 172.16.0.0/12
			return true
		case v4[0] == 192 && v4[1] == 168: // 192.168.0.0/16
			return true
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127: // CGNAT 100.64.0.0/10
			return true
		default:
			return false
		}
	}
	switch {
	case ip.IsLoopback(), ip.IsPrivate(), ip.IsUnspecified():
		return true
	default:
		return false
	}
}

// IsMetadataIP reports whether ip belongs to the link-local/metadata plane.
// Exported for callers that must classify an address without a URL in hand.
func IsMetadataIP(ip net.IP) bool { return blocked(ip, false) }

// GuardedTransport returns an http.Transport that refuses to dial blocked
// addresses. Checking at dial time, rather than only pre-validating the URL,
// closes two gaps: a hostname whose DNS answers rotate between public and
// private (rebinding), and a redirect the transport follows after the caller's
// last validation.
//
// An endpoint named in HTTP_PROXY / HTTPS_PROXY is always dialable, including
// when it is loopback — the local proxy (Clash, v2ray, mitmproxy on
// 127.0.0.1:7890) is infrastructure the user chose, and the guard applies to
// whatever the proxy then fetches on our behalf.
func GuardedTransport(strict bool) *http.Transport {
	guard := dialGuard(strict)
	endpoints := proxyEndpoints()
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if endpoints[addr] {
				return (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, addr)
			}
			return guard(ctx, network, addr)
		},
		MaxIdleConns:          20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// proxyEndpoints lists the host:port values the environment proxies point at.
func proxyEndpoints() map[string]bool {
	out := make(map[string]bool, 4)
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		v := os.Getenv(key)
		if v == "" {
			continue
		}
		u, err := url.Parse(v)
		if err != nil || u.Host == "" {
			continue
		}
		out[u.Host] = true
	}
	return out
}

// GuardedClient bundles GuardedTransport with a total request timeout, the
// shape every outbound fetch in the app wants.
func GuardedClient(timeout time.Duration, strict bool) *http.Client {
	return &http.Client{Timeout: timeout, Transport: GuardedTransport(strict)}
}

func dialGuard(strict bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("netsec: resolve %q: %w", host, err)
		}
		// An address with no usable answer is reported as the resolution
		// failure it is, so callers keep their existing error text.
		allowed := make([]net.IP, 0, len(ips))
		for _, ia := range ips {
			if !blocked(ia.IP, strict) {
				allowed = append(allowed, ia.IP)
			}
		}
		if len(allowed) == 0 {
			if len(ips) == 0 {
				return nil, fmt.Errorf("netsec: no addresses for host %q", host)
			}
			return nil, fmt.Errorf("netsec: refused to dial %q — every resolved address is in a blocked range (strict=%v)", host, strict)
		}
		// Try every permitted address before giving up, the way the stdlib
		// dialer does. Picking only the first would break a host whose A and
		// AAAA answers differ in reachability.
		var firstErr error
		for _, ip := range allowed {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			if firstErr == nil {
				firstErr = err
			}
			if ctx.Err() != nil {
				return nil, err
			}
		}
		return nil, firstErr
	}
}
