package server

import (
	"net/http"
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

// TestSandboxNetworkIsNormalised prevents an arbitrary string from reaching the
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
