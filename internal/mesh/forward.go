// Mesh forwarder — delivers locally-addressed remote messages to peer
// machines and provides the receive endpoint handler shared by the server.
package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/types"
)

// MessageStore is the persistence slice the forwarder needs.
type MessageStore interface {
	PendingForwarded(limit int) ([]types.AgentMessage, error)
	DeleteAgentMessage(id int64) error
}

// ForwardPayload is the JSON body posted to a peer's /api/mesh/messages.
type ForwardPayload struct {
	FromSession string `json:"from_session"` // sender's LOCAL session id
	ToSession   string `json:"to_session"`   // recipient session id ON the peer
	Body        string `json:"body"`
	SentAt      string `json:"sent_at"`
}

// Forwarder pushes pending remote-bound messages to peers.
type Forwarder struct {
	Store MessageStore
	// LocalOrigin names this machine in delivered FromID fields ("desktop").
	LocalOrigin string
	// Client overrides the HTTP client (tests); default 10s timeout.
	Client *http.Client
}

// Run blocks polling every interval until ctx is cancelled. Launch via xgo.
func (f *Forwarder) Run(ctx context.Context, interval time.Duration) {
	if f.Client == nil {
		f.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if f.LocalOrigin == "" {
		host, _ := os.Hostname()
		f.LocalOrigin = strings.ToLower(host)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.drainOnce(ctx)
		}
	}
}

// ensureDefaults fills zero-value fields so drainOnce is safe standalone.
func (f *Forwarder) ensureDefaults() {
	if f.Client == nil {
		f.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if f.LocalOrigin == "" {
		host, _ := os.Hostname()
		f.LocalOrigin = strings.ToLower(host)
	}
}

func (f *Forwarder) drainOnce(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	f.ensureDefaults()
	pending, err := f.Store.PendingForwarded(50)
	if err != nil || len(pending) == 0 {
		return
	}
	for _, m := range pending {
		peerName, sess, ok := splitTarget(m.ToID)
		if !ok {
			continue
		}
		peer, found := GetPeer(peerName)
		if !found {
			continue // unknown peer — leave row for later config
		}
		payload, _ := json.Marshal(ForwardPayload{
			FromSession: f.LocalOrigin + "/" + m.FromID,
			ToSession:   sess,
			Body:        m.Body,
			SentAt:      time.Now().UTC().Format(time.RFC3339),
		})
		url := strings.TrimRight(peer.URL, "/") + "/api/mesh/messages"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Mesh-Token", peer.Token)
		resp, err := f.Client.Do(req)
		if err != nil {
			log.Printf("[mesh] deliver to %s failed: %v", peerName, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = f.Store.DeleteAgentMessage(m.ID) // delivered = gone
		} else {
			log.Printf("[mesh] deliver to %s: HTTP %d", peerName, resp.StatusCode)
		}
	}
}

// ReceiveHandler validates the mesh token and stores an inbound message as a
// local agent_message with from = "<origin>@remote". Returns (session, ok).
func ReceiveHandler(store interface {
	SendAgentMessage(fromID, toID, body string) error
}, r *http.Request) (string, error) {
	token := r.Header.Get("X-Mesh-Token")
	if !VerifyToken(token) {
		return "", fmt.Errorf("invalid mesh token")
	}
	var p ForwardPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return "", fmt.Errorf("invalid body: %w", err)
	}
	if p.ToSession == "" || p.Body == "" {
		return "", fmt.Errorf("to_session and body are required")
	}
	from := p.FromSession
	if from == "" {
		from = "unknown@remote"
	}
	if !strings.HasSuffix(from, "@remote") {
		from += "@remote"
	}
	if err := store.SendAgentMessage(from, p.ToSession, p.Body); err != nil {
		return "", err
	}
	return p.ToSession, nil
}

func splitTarget(to string) (peer, session string, ok bool) {
	i := strings.IndexByte(to, '/')
	if i <= 0 || i == len(to)-1 {
		return "", "", false
	}
	return to[:i], to[i+1:], true
}
