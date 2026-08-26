package mesh

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPingPeerOnlineAndOffline(t *testing.T) {
	withHome(t)
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Mesh-Token")
		if r.URL.Path != "/api/mesh/ping" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true,"host":"box1","version":"0.42.4"}`))
	}))
	defer srv.Close()

	online := Peer{Name: "up", URL: srv.URL, Token: "tok"}
	st := PingPeer(context.Background(), online)
	if !st.Online || st.Host != "box1" || st.Version != "0.42.4" {
		t.Fatalf("status = %+v", st)
	}
	if gotToken != "tok" {
		t.Errorf("token header = %q", gotToken)
	}

	down := Peer{Name: "down", URL: "http://127.0.0.1:1", Token: "x"}
	st2 := PingPeer(context.Background(), down)
	if st2.Online || st2.Err == "" {
		t.Errorf("offline peer = %+v", st2)
	}
}

func TestRenderPeerStatuses(t *testing.T) {
	out := RenderPeerStatuses([]PeerStatus{
		{Name: "a", URL: "http://a", Online: true, Version: "1.0", Host: "ha"},
		{Name: "b", URL: "http://b", Err: "connection refused"},
	})
	if !strings.Contains(out, "✅") || !strings.Contains(out, "a") || !strings.Contains(out, "1.0") {
		t.Errorf("online line missing: %s", out)
	}
	if !strings.Contains(out, "❌") || !strings.Contains(out, "离线") || !strings.Contains(out, "connection refused") {
		t.Errorf("offline line missing: %s", out)
	}
}

func TestPingAllUsesConfiguredPeers(t *testing.T) {
	withHome(t)
	_ = UpsertPeer(Peer{Name: "nobody", URL: "http://127.0.0.1:1"})
	sts := PingAll(context.Background())
	if len(sts) != 1 || sts[0].Name != "nobody" || sts[0].Online {
		t.Fatalf("PingAll = %+v", sts)
	}
}
