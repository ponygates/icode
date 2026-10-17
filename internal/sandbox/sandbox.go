// Package sandbox confines model-issued shell commands to an operating-system
// enforced boundary.
//
// The permission gate and the bash rule engine both inspect the command string
// before the process starts; a command that is not on a deny list runs with the
// full privileges of the user. This package moves the boundary to where the
// operating system enforces it, so obfuscated commands cannot step outside it.
//
// Backends are per-platform: AppContainer on Windows, bubblewrap (falling back
// to Landlock) on Linux, sandbox-exec on macOS. A nil Runner means "not
// confined" — callers must never assume confinement happened merely because a
// configuration value asked for it.
package sandbox

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// Mode controls whether confinement is attempted, required, or skipped.
type Mode string

const (
	// ModeAuto enables confinement when the platform supports it and falls
	// back with a visible notice otherwise. This is the default: it keeps
	// existing setups working while never claiming a boundary it does not have.
	ModeAuto Mode = "auto"
	// ModeOn refuses to run shell commands when no backend is available.
	ModeOn Mode = "on"
	// ModeOff disables confinement. Callers are expected to surface a warning.
	ModeOff Mode = "off"
)

// ParseMode maps a configuration string onto a Mode. Unknown values fall back
// to ModeAuto so a typo can never silently disable the sandbox.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "required", "true", "1":
		return ModeOn
	case "off", "disabled", "false", "0", "none":
		return ModeOff
	default:
		return ModeAuto
	}
}

// NetworkPolicy is the default network posture inside the boundary.
type NetworkPolicy string

const (
	// NetworkDeny blocks outbound connections. AppContainer has no network
	// capability unless one is granted, so this is the natural default.
	NetworkDeny NetworkPolicy = "deny"
	// NetworkAllow grants the platform's client capability. It is a coarse
	// switch: per-host rules are not expressible here.
	NetworkAllow NetworkPolicy = "allow"
)

// ParseNetwork maps a configuration string onto a NetworkPolicy, defaulting to
// deny.
func ParseNetwork(s string) NetworkPolicy {
	if strings.EqualFold(strings.TrimSpace(s), string(NetworkAllow)) {
		return NetworkAllow
	}
	return NetworkDeny
}

var (
	// ErrUnsupported reports that no backend exists for this platform.
	ErrUnsupported = errors.New("sandbox: no kernel sandbox available on this platform")
	// ErrDisabled reports that confinement was switched off in configuration.
	ErrDisabled = errors.New("sandbox: confinement disabled by configuration")
)

// Spec describes one confined invocation.
type Spec struct {
	// Dir is the command's working directory. Backends treat it as writable
	// and grant access on demand.
	Dir string
	// Writable lists extra directories the command may write to. System temp
	// is added automatically because compilers and package managers need it.
	Writable []string
	// Network is the network posture for this invocation.
	Network NetworkPolicy
	// OnOutput receives output chunks as they arrive. The same bytes are also
	// accumulated into Result.Output, so callers that only want the final text
	// can leave this nil.
	OnOutput func([]byte)
}

// Result is the outcome of a confined invocation.
type Result struct {
	Output   []byte
	ExitCode int
}

// Handle is a running confined process.
type Handle interface {
	// Wait blocks until the process exits, its output is drained, and returns
	// the collected result.
	Wait() (Result, error)
	// Kill terminates the process. It is safe to call more than once and after
	// the process has already exited.
	Kill() error
}

// Runner is a ready-to-use backend. Availability is decided once at startup so
// every call site shares the same answer.
type Runner interface {
	// Name identifies the mechanism, e.g. "appcontainer". It is shown to the
	// user in status output, so it must describe what actually ran.
	Name() string
	// Available reports whether the backend can confine processes right now.
	Available() bool
	// Start launches argv confined. It returns quickly; the caller waits on
	// the returned Handle. argv is a vector rather than a string because the
	// POSIX backends exec the sandbox binary directly with the command as
	// trailing arguments.
	Start(ctx context.Context, argv []string, spec Spec) (Handle, error)
	// Close releases backend resources (profiles, temp files).
	Close() error
}

// Config is the persisted sandbox configuration, resolved into a Runner by Init.
type Config struct {
	Mode          Mode
	WritablePaths []string
	Network       NetworkPolicy
	// GrantWorkspace allows the backend to add an access-control entry for the
	// working directory. Without it the confined process cannot even use the
	// project directory as its cwd, so it defaults to true — but the notice is
	// surfaced to the user, never applied silently.
	GrantWorkspace bool
}

// active holds the process-wide backend. A typed box avoids the atomic.Value
// rule against storing nil interfaces.
var active atomic.Pointer[runnerBox]

type runnerBox struct{ r Runner }

// SetActive installs the process-wide backend. Passing nil disables
// confinement for subsequent commands.
func SetActive(r Runner) {
	if r == nil {
		active.Store(nil)
		return
	}
	active.Store(&runnerBox{r: r})
}

// Active returns the installed backend, or nil when commands run unconfined.
func Active() Runner {
	b := active.Load()
	if b == nil {
		return nil
	}
	return b.r
}

// defaultWritablePaths returns cache directories a confined process needs but
// the user never had to name. Go's caches are included because without them
// every `go build` inside the boundary fails, which would make the sandbox
// unusable in practice. The system temp directory is deliberately NOT included:
// granting the whole of %TEMP% would expose every other process's scratch files,
// so the Windows backend hands the child a dedicated temp directory instead.
func defaultWritablePaths() []string {
	seen := make(map[string]bool, 4)
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		add(os.Getenv(key))
	}
	return out
}

// Init resolves cfg into a Runner, installs it, and returns human-readable
// notices the caller should surface. A nil Runner with a nil error means "run
// unconfined, and say so".
func Init(cfg Config) (runner Runner, notices []string, err error) {
	cfg.WritablePaths = append(cfg.WritablePaths, defaultWritablePaths()...)
	// Record what actually happened before any early return, so Status() can
	// never describe a boundary that was not installed.
	defer func() { recordInit(cfg, runner, notices) }()

	if cfg.Mode == ModeOff {
		SetActive(nil)
		return nil, []string{
			"sandbox is off: model-issued commands run with your full user privileges",
		}, nil
	}

	r, n, rerr := newPlatformRunner(cfg)
	if rerr != nil || r == nil || !r.Available() {
		if rerr != nil {
			n = append(n, rerr.Error())
		}
		if cfg.Mode == ModeOn {
			SetActive(nil)
			return nil, n, ErrUnsupported
		}
		SetActive(nil)
		n = append(n, "no kernel sandbox available: commands run unconfined")
		return nil, n, nil
	}

	SetActive(r)
	return r, n, nil
}

// StatusInfo is a description of the confinement actually in force. Settings UI and
// status output read this instead of the configuration, because a configured
// sandbox and an installed one are not the same thing.
type StatusInfo struct {
	Enabled   bool     `json:"enabled"`
	Backend   string   `json:"backend,omitempty"`
	Available bool     `json:"available"`
	Mode      string   `json:"mode"`
	Network   string   `json:"network"`
	Notices   []string `json:"notices,omitempty"`
}

var initState struct {
	mu      sync.Mutex
	mode    Mode
	network NetworkPolicy
	notices []string
}

func recordInit(cfg Config, runner Runner, notices []string) {
	initState.mu.Lock()
	initState.mode = cfg.Mode
	initState.network = cfg.Network
	initState.notices = append([]string(nil), notices...)
	initState.mu.Unlock()
}

// Status reports the confinement in force right now.
func Status() StatusInfo {
	st := StatusInfo{}
	initState.mu.Lock()
	st.Mode = string(initState.mode)
	st.Network = string(initState.network)
	st.Notices = append([]string(nil), initState.notices...)
	initState.mu.Unlock()
	if st.Mode == "" {
		st.Mode = string(ModeAuto)
	}
	if st.Network == "" {
		st.Network = string(NetworkDeny)
	}
	if r := Active(); r != nil {
		st.Enabled = true
		st.Backend = r.Name()
		st.Available = r.Available()
	}
	if msg := recordFailureNotice(); msg != "" {
		st.Notices = append(st.Notices, msg)
	}
	return st
}
