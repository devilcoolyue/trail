package main

import (
	"strings"
	"testing"
)

func TestSetAlternateScreen(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "[tui]\nalternate_screen = \"never\"\n"},
		{"model = \"o3\"", "model = \"o3\"\n\n[tui]\nalternate_screen = \"never\"\n"},
		{"[tui]\nanimations = false\n\n[mcp]\nx = 1\n", "[tui]\nalternate_screen = \"never\"\nanimations = false\n\n[mcp]\nx = 1\n"},
		{"[tui]\n  alternate_screen = \"auto\"\n", "[tui]\nalternate_screen = \"never\"\n"},
		// 别的表里的同名键不算
		{"[x]\nalternate_screen = 1\n[tui]\n", "[x]\nalternate_screen = 1\n[tui]\nalternate_screen = \"never\"\n"},
	}
	for _, c := range cases {
		if got := setAlternateScreen(c.in); got != c.want {
			t.Errorf("setAlternateScreen(%q) =\n%q\nwant\n%q", c.in, got, c.want)
		}
		if got := setAlternateScreen(c.want); got != c.want {
			t.Errorf("second run changed %q to %q", c.want, got)
		}
	}
}

func TestAddTmuxBindings(t *testing.T) {
	popup := "display-popup -E -w 92% -h 88% /usr/local/bin/trail"
	got := addTmuxBindings("set -g mouse on", popup)
	for _, want := range []string{"set -g mouse on\n\n# trail：", "bind-key t " + popup + "\n", "bind-key -n C-] " + popup + "\n", "history-limit 200000"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if again := addTmuxBindings(got, popup); again != got {
		t.Errorf("second run changed the file:\n%s", again)
	}
	if got := addTmuxBindings("set-option -g history-limit 5000\n", popup); strings.Contains(got, "200000") {
		t.Errorf("overrode the user's history-limit:\n%s", got)
	}
}
