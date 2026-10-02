//go:build windows && !nogui

package cmd

import "testing"

func TestValidateWindowState(t *testing.T) {
	cases := []struct {
		name string
		st   windowState
		want bool
	}{
		{"normal", windowState{X: 100, Y: 100, W: 1200, H: 820}, true},
		{"maximized", windowState{X: -8, Y: -8, W: 1936, H: 1056, Maximized: true}, true},
		{"collapsed", windowState{W: 0, H: 0}, false},
		{"too narrow", windowState{W: 200, H: 800}, false},
		{"too short", windowState{W: 800, H: 100}, false},
		{"absurd wide", windowState{W: 99999, H: 800}, false},
		{"absurd tall", windowState{W: 800, H: 99999}, false},
		{"negative size", windowState{X: 50, Y: 50, W: -400, H: -300}, false},
	}
	for _, c := range cases {
		if got := validateWindowState(c.st); got != c.want {
			t.Errorf("%s: validateWindowState(%+v) = %v, want %v", c.name, c.st, got, c.want)
		}
	}
}
