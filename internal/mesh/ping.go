// Mesh connectivity — pings configured peers and renders one status line per
// peer, shared by the /mesh command on all three surfaces.
package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// PeerStatus is one peer's reachability snapshot.
type PeerStatus struct {
	Name    string
	URL     string
	Online  bool
	Version string
	Host    string
	Err     string
}

// PingPeer probes a single peer's /api/mesh/ping endpoint.
func PingPeer(ctx context.Context, p Peer) PeerStatus {
	st := PeerStatus{Name: p.Name, URL: p.URL}
	body, err := json.Marshal(map[string]string{})
	if err != nil {
		st.Err = err.Error()
		return st
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.URL, "/")+"/api/mesh/ping", strings.NewReader(string(body)))
	if err != nil {
		st.Err = err.Error()
		return st
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Mesh-Token", p.Token)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		st.Err = err.Error()
		return st
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		st.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return st
	}
	var out struct {
		Host    string `json:"host"`
		Version string `json:"version"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	st.Online = true
	st.Host = out.Host
	st.Version = out.Version
	return st
}

// PingAll probes every configured peer sequentially (few peers, 3s cap each).
func PingAll(ctx context.Context) []PeerStatus {
	peers, _ := LoadPeers()
	out := make([]PeerStatus, 0, len(peers))
	for _, p := range peers {
		out = append(out, PingPeer(ctx, p))
	}
	return out
}

// RenderPeerStatuses formats probe results for chat surfaces.
func RenderPeerStatuses(sts []PeerStatus) string {
	var b strings.Builder
	for _, s := range sts {
		if s.Online {
			v := s.Version
			if v == "" {
				v = "?"
			}
			fmt.Fprintf(&b, "  ✅ %-12s %s  v%s（主机 %s）\n", s.Name, s.URL, v, s.Host)
		} else {
			reason := s.Err
			if reason == "" {
				reason = "无响应"
			}
			fmt.Fprintf(&b, "  ❌ %-12s %s  离线（%s）\n", s.Name, s.URL, reason)
		}
	}
	return b.String()
}
