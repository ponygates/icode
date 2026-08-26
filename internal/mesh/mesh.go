// Package mesh — cross-machine message sync for iCode sessions. Two iCode
// instances (e.g. a desktop and a laptop) exchange agent messages over their
// existing HTTP servers:
//
//	addressing   to_session "<peer>/<sessionID>" routes the message off-machine
//	forwarder    polls local SQLite every 3s, POSTs pending rows to the peer,
//	             deletes them on 2xx ACK (delivered = gone)
//	receiver     POST /api/mesh/messages stores inbound rows with
//	             from_session "<peerName>@remote"
//	auth         shared secret in ~/.icode/mesh.token; peers must present it
//	             via X-Mesh-Token. Peers listed in ~/.icode/peers.yaml.
package mesh

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Peer is one remote iCode instance.
type Peer struct {
	Name  string `yaml:"name"`  // local alias, used in "<peer>/<session>" addressing
	URL   string `yaml:"url"`   // e.g. http://192.168.1.20:8787
	Token string `yaml:"token"` // shared mesh token of THAT machine
}

type peerFile struct {
	Peers []Peer `yaml:"peers"`
}

func peersPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".icode", "peers.yaml")
}

// TokenPath returns the local mesh auth token location.
func TokenPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".icode", "mesh.token")
}

// EnsureToken returns this machine's mesh token, generating one on first use.
func EnsureToken() (string, error) {
	path := TokenPath()
	if data, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(data)); tok != "" {
			return tok, nil
		}
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

// VerifyToken checks an incoming X-Mesh-Token against the local token.
func VerifyToken(presented string) bool {
	want, err := EnsureToken()
	if err != nil {
		return false
	}
	return presented == want
}

// LoadPeers reads ~/.icode/peers.yaml sorted by name.
func LoadPeers() ([]Peer, error) {
	data, err := os.ReadFile(peersPath())
	if err != nil {
		return nil, nil // no peers yet — not an error
	}
	var pf peerFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", peersPath(), err)
	}
	sort.Slice(pf.Peers, func(i, j int) bool { return pf.Peers[i].Name < pf.Peers[j].Name })
	return pf.Peers, nil
}

// SavePeers writes the peer list back.
func SavePeers(peers []Peer) error {
	if err := os.MkdirAll(filepath.Dir(peersPath()), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(peerFile{Peers: peers})
	if err != nil {
		return err
	}
	return os.WriteFile(peersPath(), data, 0o600)
}

// UpsertPeer adds or replaces a peer by name.
func UpsertPeer(p Peer) error {
	peers, _ := LoadPeers()
	replaced := false
	for i := range peers {
		if peers[i].Name == p.Name {
			peers[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		peers = append(peers, p)
	}
	return SavePeers(peers)
}

// RemovePeer deletes a peer by name; ok=false when absent.
func RemovePeer(name string) (bool, error) {
	peers, _ := LoadPeers()
	out := peers[:0]
	found := false
	for _, p := range peers {
		if p.Name == name {
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		return false, nil
	}
	return true, SavePeers(out)
}

// GetPeer resolves an alias to its connection info.
func GetPeer(name string) (*Peer, bool) {
	peers, _ := LoadPeers()
	for i := range peers {
		if peers[i].Name == name {
			return &peers[i], true
		}
	}
	return nil, false
}

// SplitTarget splits "peer/session" addressing. isRemote=false for plain
// local session ids.
func SplitTarget(to string) (peer string, session string, isRemote bool) {
	to = strings.TrimSpace(to)
	i := strings.IndexByte(to, '/')
	if i <= 0 || i == len(to)-1 {
		return "", to, false
	}
	return to[:i], to[i+1:], true
}
