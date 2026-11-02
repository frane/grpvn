package tests

import (
	"strings"
	"testing"
)

func TestPrivateAndOpenCoordinatorCLI(t *testing.T) {
	t.Parallel()
	host := newRunner(t, "host")
	for _, args := range [][]string{
		{"sv", "--listen", "127.0.0.1:0", "--open", "--private", "--token", "secret"},
		{"sv", "--listen", "127.0.0.1:0", "--token", "secret"},
		{"sv", "--open"},
	} {
		if _, _, code := host.run(args...); code == 0 {
			t.Fatalf("invalid access options succeeded: %v", args)
		}
	}
	endpoint, _ := startCoordinatorArgs(t, host, "--open")
	client := newRunner(t, "guest")
	client.mustRun("co", "public", endpoint, "#lobby")
	client.mustRun("s", "#public/lobby", "anyone can post")
	if out := host.mustRun("l", "#lobby"); !strings.Contains(out, "anyone can post") {
		t.Fatalf("open channel write failed: %s", out)
	}
}
