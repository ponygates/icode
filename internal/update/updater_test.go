package update

import "testing"

func TestVersionGreater(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.37.0", "0.36.0", true},
		{"1.0.0", "0.36.0", true},
		{"0.36.1", "0.36.0", true},
		{"0.36.0", "0.37.0", false},
		{"0.36.0", "0.36.0", false},
		{"0.35.9", "0.36.0", false},
		{"1.0.0", "1.0.0", false},
		{"0.36", "0.36.0", false},
	}
	for _, c := range cases {
		if got := versionGreater(c.a, c.b); got != c.want {
			t.Errorf("versionGreater(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	if got := parseVersion("0.36.0"); got != [3]int{0, 36, 0} {
		t.Fatalf("parseVersion = %v", got)
	}
	if got := parseVersion("1.2"); got != [3]int{1, 2, 0} {
		t.Fatalf("parseVersion(1.2) = %v", got)
	}
	if got := parseVersion("garbage"); got != [3]int{0, 0, 0} {
		t.Fatalf("parseVersion(garbage) = %v", got)
	}
}
