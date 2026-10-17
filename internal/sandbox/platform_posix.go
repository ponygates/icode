//go:build linux || darwin

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The POSIX backends work by running a sandbox helper as a wrapper around the
// real command: bubblewrap on Linux, sandbox-exec on macOS. Everything that is
// not about constructing the wrapper — spawning, pipes, output accumulation,
// cancellation — lives in this file so it exists exactly once.
//
// These backends do not touch the filesystem's permission metadata. Where the
// Windows backend has to add an ACE to a directory before a confined process
// can enter it, bwrap and sandbox-exec express the same permission in the
// process's own mount/namespace view. That is why no grant record is written
// here: nothing was changed that would need undoing.

// posixRunner is a wrapper-based backend. A non-nil runner is always usable:
// the constructor only returns one after its self-test has run, so Available
// never has to guess.
//
// It holds no temporary directory of its own. Linux gives each invocation a
// private tmpfs at /tmp; macOS hands every user a private 0700 directory in
// $TMPDIR already. Creating a third one would only leave litter behind.
type posixRunner struct {
	name    string
	helper  string
	network NetworkPolicy
	extra   []string
	wrap    func(r *posixRunner, dir string, argv []string, spec Spec) ([]string, []string, error)
}

func (r *posixRunner) Name() string    { return r.name }
func (r *posixRunner) Available() bool { return true }
func (r *posixRunner) Close() error    { return nil }

// Start launches argv through the wrapper.
func (r *posixRunner) Start(ctx context.Context, argv []string, spec Spec) (Handle, error) {
	if len(argv) == 0 {
		return nil, errors.New("sandbox: empty command")
	}
	dir := spec.Dir
	if dir == "" {
		// An empty Dir would make the wrapper inherit the caller's working
		// directory, which may not be inside the boundary at all.
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("sandbox: cannot determine the working directory: %w", err)
		}
		dir = wd
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("sandbox: %q is not a usable working directory", dir)
	}

	full, env, err := r.wrap(r, dir, argv, spec)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(full[0], full[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	buf := &outBuf{on: spec.OnOutput}
	cmd.Stdout = buf
	cmd.Stderr = buf
	// A dedicated process group lets Kill reach a wrapper's children. bwrap
	// always has at least one; killing only the wrapper would orphan it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("sandbox: start %s: %w", r.name, err)
	}

	h := &posixHandle{cmd: cmd, buf: buf, done: make(chan struct{})}
	go h.pump()
	go func() {
		select {
		case <-ctx.Done():
			_ = h.Kill()
		case <-h.done:
		}
	}()
	return h, nil
}

// posixHandle is a running wrapped process.
type posixHandle struct {
	cmd      *exec.Cmd
	buf      *outBuf
	done     chan struct{}
	res      Result
	err      error
	killOnce sync.Once
}

// Wait blocks until the process exits and its output is fully drained. A
// non-zero exit is reported through Result.ExitCode, not as an error: callers
// distinguish "the command failed" from "the boundary could not run it".
func (h *posixHandle) Wait() (Result, error) {
	<-h.done
	return h.res, h.err
}

// Kill terminates the process group.
func (h *posixHandle) Kill() error {
	h.killOnce.Do(func() {
		if h.cmd.Process == nil {
			return
		}
		// The negative pid targets the group created by Setpgid. If that
		// fails — the process already exited, or the group is gone — fall
		// back to the single process so Kill never silently does nothing.
		if err := syscall.Kill(-h.cmd.Process.Pid, syscall.SIGKILL); err != nil {
			_ = h.cmd.Process.Kill()
		}
	})
	return nil
}

func (h *posixHandle) pump() {
	err := h.cmd.Wait()
	h.res.Output = h.buf.Bytes()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code := ee.ExitCode()
			if code < 0 {
				// Killed by a signal. Report the shell convention (128+n)
				// so callers that print "exit status N" stay meaningful.
				if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
					code = 128 + int(ws.Signal())
				} else {
					code = 1
				}
			}
			h.res.ExitCode = code
			err = nil
		}
	}
	h.err = err
	close(h.done)
}

// outBuf accumulates output and forwards it live. It is passed as both Stdout
// and Stderr, which os/exec recognises as one writer and therefore services
// from a single goroutine — so chunks never interleave mid-line.
type outBuf struct {
	mu sync.Mutex
	b  []byte
	on func([]byte)
}

func (o *outBuf) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.b = append(o.b, p...)
	o.mu.Unlock()
	// The callback runs outside the lock: it writes into a TUI or an HTTP
	// stream, and doing that under our lock would serialise the whole
	// transcript behind a slow consumer.
	if o.on != nil {
		o.on(p)
	}
	return len(p), nil
}

func (o *outBuf) Bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.b...)
}

// writableDirs resolves the directories the confined process may write to:
// the working directory, whatever the caller asked for, and the cache
// directories resolved at startup. Non-existent paths are dropped because the
// wrappers refuse to bind a source that is not there.
func writableDirs(dir string, spec Spec, r *posixRunner) []string {
	seen := make(map[string]bool, 8)
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		abs = filepath.Clean(abs)
		if abs == "/" || abs == string(filepath.Separator) || seen[abs] {
			return
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return
		}
		seen[abs] = true
		out = append(out, abs)
	}
	add(dir)
	for _, p := range spec.Writable {
		add(p)
	}
	for _, p := range r.extra {
		add(p)
	}
	return out
}

// envWithTmp returns the parent environment with TMPDIR pointed at dir. The
// wrappers give each invocation a private temp directory; leaving TMPDIR alone
// would send writes to the real /tmp, which is not writable inside.
func envWithTmp(base []string, dir string) []string {
	if dir == "" {
		return base
	}
	out := make([]string, 0, len(base)+1)
	for _, e := range base {
		if strings.HasPrefix(e, "TMPDIR=") || strings.HasPrefix(e, "TMP=") || strings.HasPrefix(e, "TEMP=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, "TMPDIR="+dir)
}

// resolveHelper finds the sandbox binary. A missing helper is not fatal: it
// only means this machine cannot confine anything, which Init reports.
func resolveHelper(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("sandbox: %s not found in PATH", name)
	}
	return path, nil
}

// runProbe executes cmd with a short timeout and turns a failure into a
// diagnosis. The message includes the helper's own output because the most
// common Linux failure — unprivileged user namespaces disabled — is only
// recognisable from what bwrap prints.
func runProbe(bin string, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	return fmt.Errorf("%s", msg)
}
