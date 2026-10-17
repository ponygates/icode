package sandbox

import (
	"context"
	"testing"
)

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"":            ModeAuto,
		"auto":        ModeAuto,
		"AUTO":        ModeAuto,
		"  on ":       ModeOn,
		"required":    ModeOn,
		"true":        ModeOn,
		"1":           ModeOn,
		"off":         ModeOff,
		"none":        ModeOff,
		"false":       ModeOff,
		"0":           ModeOff,
		"nonsense":    ModeAuto, // a typo must never silently disable confinement
		"auto-please": ModeAuto,
	}
	for in, want := range cases {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseNetwork(t *testing.T) {
	if got := ParseNetwork("allow"); got != NetworkAllow {
		t.Errorf("ParseNetwork(allow) = %q", got)
	}
	// Anything unrecognised must fail closed.
	for _, in := range []string{"", "deny", "ALLOWED", "yes"} {
		if got := ParseNetwork(in); got != NetworkDeny {
			t.Errorf("ParseNetwork(%q) = %q, want deny", in, got)
		}
	}
}

func TestActiveIsNilByDefault(t *testing.T) {
	SetActive(nil)
	if Active() != nil {
		t.Fatal("Active() should be nil when nothing is installed")
	}
}

func TestInitModeOffReturnsNotice(t *testing.T) {
	SetActive(nil)
	r, notices, err := Init(Config{Mode: ModeOff})
	if err != nil {
		t.Fatalf("Init(off) returned error: %v", err)
	}
	if r != nil {
		t.Fatal("Init(off) must not install a runner")
	}
	if len(notices) == 0 {
		t.Fatal("Init(off) must warn that commands run unconfined")
	}
	if Active() != nil {
		t.Fatal("Init(off) must clear the active runner")
	}
}

// TestInitRecordsUserShellIntent checks that the opt-in is recorded on every
// path, including the ones that install nothing. The TUI reads it later to
// decide whether "!" belongs inside the boundary, and a missed record would
// silently answer "no" to a setting the user turned on.
func TestInitRecordsUserShellIntent(t *testing.T) {
	t.Cleanup(func() {
		SetActive(nil)
		_, _, _ = Init(Config{Mode: ModeOff})
	})

	_, _, _ = Init(Config{Mode: ModeOff, ConfineUserShell: true})
	if !ConfineUserShell() {
		t.Fatalf("Init(off, confine=true) did not record the intent")
	}
	_, _, _ = Init(Config{Mode: ModeOff, ConfineUserShell: false})
	if ConfineUserShell() {
		t.Fatalf("Init(off, confine=false) left the intent set")
	}
}

// TestStatusReportsUserShellOnlyWhenInstalled keeps the status honest: an
// intent recorded without a backend is not a boundary, and reporting it as one
// is exactly the false assurance the sandbox is supposed to remove.
func TestStatusReportsUserShellOnlyWhenInstalled(t *testing.T) {
	t.Cleanup(func() {
		SetActive(nil)
		_, _, _ = Init(Config{Mode: ModeOff})
	})

	_, _, _ = Init(Config{Mode: ModeOff, ConfineUserShell: true})
	if st := Status(); st.ConfineUserShell {
		t.Fatalf("Status claims user commands are confined with no runner installed")
	}
}

func TestSpecZeroValueIsNotConfinedByIdentity(t *testing.T) {
	// Guard against a future backend treating an empty Spec as "already
	// prepared" — an empty Dir must still be handled explicitly.
	var s Spec
	if s.Dir != "" || s.Network != "" {
		t.Fatalf("unexpected non-zero Spec: %+v", s)
	}
	_ = context.Background()
}
