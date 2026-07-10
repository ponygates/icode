package conversation

import "testing"

// TestTextAccumulator verifies the smart-delta logic correctly handles both
// incremental and cumulative relay streaming without repetition.
func TestTextAccumulator(t *testing.T) {
	// Incremental stream: each chunk is a new fragment.
	inc := []string{"He", "ll", "o ", "wor", "ld"}
	acc := &textAccumulator{}
	var incFull string
	for _, c := range inc {
		f, d := acc.feed(c)
		incFull = f
		if d != c {
			t.Errorf("incremental: expected delta=%q, got %q", c, d)
		}
	}
	if incFull != "Hello world" {
		t.Errorf("incremental full = %q, want %q", incFull, "Hello world")
	}

	// Cumulative stream: each chunk repeats everything so far.
	cum := []string{"He", "Hello", "Hello world", "Hello world!"}
	acc2 := &textAccumulator{}
	var cumFull string
	var emitted []string
	for _, c := range cum {
		f, d := acc2.feed(c)
		cumFull = f
		emitted = append(emitted, d)
	}
	wantEmitted := []string{"He", "llo", " world", "!"}
	if len(emitted) != len(wantEmitted) {
		t.Fatalf("cumulative emitted %d deltas, want %d", len(emitted), len(wantEmitted))
	}
	for i := range wantEmitted {
		if emitted[i] != wantEmitted[i] {
			t.Errorf("cumulative delta[%d] = %q, want %q", i, emitted[i], wantEmitted[i])
		}
	}
	if cumFull != "Hello world!" {
		t.Errorf("cumulative full = %q, want %q", cumFull, "Hello world!")
	}
}
