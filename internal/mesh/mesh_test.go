package mesh

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ponygates/icode/internal/types"
)

func withHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := os.Getenv("USERPROFILE")
	if old == "" {
		old = os.Getenv("HOME")
	}
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
	t.Cleanup(func() {
		os.Setenv("USERPROFILE", old)
		os.Setenv("HOME", old)
	})
	return dir
}

type fakeMsgStore struct {
	sent    []types.AgentMessage
	deleted []int64
	pending []types.AgentMessage
}

func (f *fakeMsgStore) SendAgentMessage(from, to, body string) error {
	f.sent = append(f.sent, types.AgentMessage{FromID: from, ToID: to, Body: body})
	return nil
}
func (f *fakeMsgStore) PendingForwarded(limit int) ([]types.AgentMessage, error) {
	if len(f.pending) > limit {
		return f.pending[:limit], nil
	}
	return f.pending, nil
}
func (f *fakeMsgStore) DeleteAgentMessage(id int64) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func TestEnsureTokenStable(t *testing.T) {
	withHome(t)
	a, err := EnsureToken()
	if err != nil || a == "" {
		t.Fatalf("EnsureToken: %v %q", err, a)
	}
	b, _ := EnsureToken()
	if a != b {
		t.Error("token must be stable across calls")
	}
	if !VerifyToken(a) || VerifyToken("wrong") {
		t.Error("VerifyToken mismatch")
	}
}

func TestPeerCRUD(t *testing.T) {
	withHome(t)
	if peers, _ := LoadPeers(); len(peers) != 0 {
		t.Fatal("fresh home should have no peers")
	}
	if err := UpsertPeer(Peer{Name: "laptop", URL: "http://x:1"}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertPeer(Peer{Name: "desktop", URL: "http://y:2"}); err != nil {
		t.Fatal(err)
	}
	UpsertPeer(Peer{Name: "laptop", URL: "http://x:9"}) // replace
	peers, _ := LoadPeers()
	if len(peers) != 2 || peers[0].Name != "desktop" || peers[1].URL != "http://x:9" {
		t.Fatalf("peers = %+v", peers)
	}
	ok, _ := RemovePeer("laptop")
	if !ok {
		t.Error("remove existing should return true")
	}
	if ok, _ := RemovePeer("ghost"); ok {
		t.Error("remove missing should return false")
	}
}

func TestSplitTarget(t *testing.T) {
	p, s, remote := SplitTarget("laptop/sess-1")
	if !remote || p != "laptop" || s != "sess-1" {
		t.Errorf("got %q %q %v", p, s, remote)
	}
	_, _, remote = SplitTarget("localsession")
	if remote {
		t.Error("plain id must not be remote")
	}
	_, _, remote = SplitTarget("/leading")
	if remote {
		t.Error("empty peer is not valid remote addressing")
	}
}

// The forwarder delivers pending rows to the peer and deletes on ACK.
func TestForwarderDrain(t *testing.T) {
	withHome(t)
	received := make(chan ForwardPayload, 4)
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Mesh-Token")
		var p ForwardPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		received <- p
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := UpsertPeer(Peer{Name: "peer-a", URL: srv.URL, Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	store := &fakeMsgStore{pending: []types.AgentMessage{
		{ID: 7, FromID: "sess-local", ToID: "peer-a/sess-remote", Body: "hello over mesh"},
		{ID: 8, FromID: "sess-local", ToID: "unknown-peer/sess-x", Body: "kept — no such peer"},
	}}
	f := &Forwarder{Store: store, LocalOrigin: "testbox"}
	f.drainOnce(nil)

	select {
	case p := <-received:
		if p.ToSession != "sess-remote" || p.Body != "hello over mesh" {
			t.Errorf("payload = %+v", p)
		}
		if !strings.HasPrefix(p.FromSession, "testbox/") {
			t.Errorf("FromSession = %q, want testbox/-prefixed", p.FromSession)
		}
		if gotToken != "secret" {
			t.Errorf("token header = %q", gotToken)
		}
	default:
		t.Fatal("forwarder did not POST to peer")
	}
	// Delivered row deleted; unknown-peer row kept.
	if len(store.deleted) != 1 || store.deleted[0] != 7 {
		t.Errorf("deleted = %v", store.deleted)
	}
}
