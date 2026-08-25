package notify

import "testing"

func TestPsQuote(t *testing.T) {
	cases := map[string]string{
		"hello":     "'hello'",
		"it's done": "'it''s done'",
		"后台任务":      "'后台任务'",
		"a'b'c":     "'a''b''c'",
	}
	for in, want := range cases {
		if got := psQuote(in); got != want {
			t.Fatalf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShouldNotify(t *testing.T) {
	SetPolicy(true, "", "")
	if !shouldNotify() {
		t.Fatal("enabled with no window should notify")
	}
	SetPolicy(false, "", "")
	if shouldNotify() {
		t.Fatal("disabled should not notify")
	}
	// In-window vs out-of-window (fixed times, no midnight wrap).
	SetPolicy(true, "00:00", "23:59")
	if shouldNotify() {
		t.Fatal("inside full-day window should be suppressed")
	}
	SetPolicy(true, "00:00", "00:01")
	// 23:59 is outside 00:00–00:01.
	if !shouldNotify() {
		t.Fatal("outside tiny window should notify")
	}
	// Reset for other tests.
	SetPolicy(true, "", "")
}
