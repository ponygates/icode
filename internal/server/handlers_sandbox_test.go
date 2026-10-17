package server

import (
	"net/http"
	"runtime"
	"testing"
)

// TestSandboxStatusSeparatesRuntimeFromConfig pins the distinction the
// settings page depends on: what is stored is not necessarily what is enforced.
// A UI that read the configuration alone would claim a boundary the process
// does not have.
func TestSandboxStatusSeparatesRuntimeFromConfig(t *testing.T) {
	isolateHome(t)
	_, base := newSecurityTestServer(t)

	var body struct {
		Runtime struct {
			Enabled bool   `json:"enabled"`
			Backend string `json:"backend"`
			Mode    string `json:"mode"`
			Network string `json:"network"`
		} `json:"runtime"`
		Config struct {
			Mode    string `json:"mode"`
			Network string `json:"network"`
		} `json:"config"`
		RestartRequired bool `json:"restart_required"`
	}
	httpDo(t, http.MethodGet, base+"/api/sandbox", "", http.StatusOK, &body)

	// The test binary never calls sandbox.Init, so nothing is installed even
	// though the default configuration asks for auto.
	if body.Runtime.Enabled {
		t.Fatalf("runtime claims confinement with no backend installed (backend=%q)", body.Runtime.Backend)
	}
	if body.Runtime.Mode != "auto" {
		t.Fatalf("runtime.mode = %q, want auto (the parse default)", body.Runtime.Mode)
	}
	if body.Runtime.Network != "deny" {
		t.Fatalf("runtime.network = %q, want deny", body.Runtime.Network)
	}
	if body.RestartRequired {
		t.Fatalf("a fresh status must not ask for a restart")
	}
}

// TestSandboxRejectsUnknownMode keeps a typo from silently becoming auto: the
// handler validates before persisting rather than relying on ParseMode's
// forgiving fallback.
func TestSandboxRejectsUnknownMode(t *testing.T) {
	isolateHome(t)
	_, base := newSecurityTestServer(t)

	httpDo(t, http.MethodPut, base+"/api/sandbox", `{"mode":"whatever"}`, http.StatusBadRequest, nil)
}

// TestSandboxSaveRequestsRestart documents that changing the mode cannot take
// effect in the running process: the backend, its profile and the workspace
// ACEs are built once at startup.
func TestSandboxSaveRequestsRestart(t *testing.T) {
	isolateHome(t)
	_, base := newSecurityTestServer(t)

	var saved struct {
		OK              bool `json:"ok"`
		RestartRequired bool `json:"restart_required"`
	}
	httpDo(t, http.MethodPut, base+"/api/sandbox", `{"mode":"off"}`, http.StatusOK, &saved)
	if !saved.OK {
		t.Fatalf("save did not report ok")
	}
	if !saved.RestartRequired {
		t.Fatalf("mode change must report restart_required=true")
	}

	var after struct {
		Runtime struct {
			Enabled bool   `json:"enabled"`
			Mode    string `json:"mode"`
		} `json:"runtime"`
		Config struct {
			Mode string `json:"mode"`
		} `json:"config"`
	}
	httpDo(t, http.MethodGet, base+"/api/sandbox", "", http.StatusOK, &after)
	if after.Config.Mode != "off" {
		t.Fatalf("config.mode = %q, want off", after.Config.Mode)
	}
	// The runtime has not been rebuilt, so it still reports the startup value.
	// This is the whole point of the flag: the UI must not show "off" as if it
	// were already in force.
	if after.Runtime.Mode == "off" {
		t.Fatalf("runtime.mode followed the saved config before a restart")
	}
}

// TestSandboxConfineUserShellPersists covers the third shell entry point: the
// setting has to survive a save, and the response has to admit that it needs a
// restart, because the intent is read once at startup.
func TestSandboxConfineUserShellPersists(t *testing.T) {
	isolateHome(t)
	_, base := newSecurityTestServer(t)

	type configView struct {
		Config struct {
			ConfineUserShell bool `json:"confine_user_shell"`
		} `json:"config"`
		RestartRequired bool `json:"restart_required"`
	}

	var saved configView
	httpDo(t, http.MethodPut, base+"/api/sandbox", `{"confine_user_shell":true}`, http.StatusOK, &saved)
	if !saved.Config.ConfineUserShell {
		t.Fatalf("saved response does not echo confine_user_shell=true")
	}
	if !saved.RestartRequired {
		t.Fatalf("changing the user-shell scope must report restart_required")
	}

	var after configView
	httpDo(t, http.MethodGet, base+"/api/sandbox", "", http.StatusOK, &after)
	if !after.Config.ConfineUserShell {
		t.Fatalf("confine_user_shell did not persist")
	}
}

// TestSandboxConfineUserShellDefaultsOff pins the default that keeps
// "! git push" working. Flipping it would be a silent breaking change for
// every existing user, so the default is asserted rather than assumed.
func TestSandboxConfineUserShellDefaultsOff(t *testing.T) {
	isolateHome(t)
	_, base := newSecurityTestServer(t)

	var body struct {
		Config struct {
			ConfineUserShell bool `json:"confine_user_shell"`
		} `json:"config"`
	}
	httpDo(t, http.MethodGet, base+"/api/sandbox", "", http.StatusOK, &body)
	if body.Config.ConfineUserShell {
		t.Fatalf("confine_user_shell defaults to true; user commands would break")
	}
}

// TestSandboxGrantsAreListed covers the "make the change visible" half: the
// settings page has to be able to show which directories carry an entry for
// the sandbox identity.
func TestSandboxGrantsAreListed(t *testing.T) {
	isolateHome(t)
	_, base := newSecurityTestServer(t)

	var body struct {
		Grants    []map[string]any `json:"grants"`
		Revocable bool             `json:"revocable"`
	}
	httpDo(t, http.MethodGet, base+"/api/sandbox/grants", "", http.StatusOK, &body)
	if body.Grants == nil {
		t.Fatalf("grants must be an empty array, not null: the UI renders it directly")
	}
	if !body.Revocable {
		t.Fatalf("revocable = false on %s; only the platform decides this", runtime.GOOS)
	}
}

// TestSandboxRevokeRequiresAPath keeps a malformed request from reaching the
// filesystem layer, where an empty path would resolve to the process directory.
func TestSandboxRevokeRequiresAPath(t *testing.T) {
	isolateHome(t)
	_, base := newSecurityTestServer(t)

	httpDo(t, http.MethodDelete, base+"/api/sandbox/grants", `{"path":"  "}`, http.StatusBadRequest, nil)
	httpDo(t, http.MethodDelete, base+"/api/sandbox/grants", `{"path":`, http.StatusBadRequest, nil)
}

// TestSandboxGrantsRejectOtherMethods: a GET-only resource that accepts POST
// would let a form submission appear to revoke access while doing nothing.
func TestSandboxGrantsRejectOtherMethods(t *testing.T) {
	isolateHome(t)
	_, base := newSecurityTestServer(t)

	httpDo(t, http.MethodPost, base+"/api/sandbox/grants", `{"path":"C:\\x"}`, http.StatusMethodNotAllowed, nil)
}

// backend, where it would be compared for equality and silently treated as deny
// while the settings page echoed the user's text back as if accepted.
func TestSandboxNetworkIsNormalised(t *testing.T) {
	isolateHome(t)
	_, base := newSecurityTestServer(t)

	httpDo(t, http.MethodPut, base+"/api/sandbox", `{"network":"sometimes"}`, http.StatusOK, nil)

	var after struct {
		Config struct {
			Network string `json:"network"`
		} `json:"config"`
	}
	httpDo(t, http.MethodGet, base+"/api/sandbox", "", http.StatusOK, &after)
	if after.Config.Network != "deny" {
		t.Fatalf("network = %q, want deny", after.Config.Network)
	}
}
