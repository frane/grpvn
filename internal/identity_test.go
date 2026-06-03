package internal

import (
	"strings"
	"testing"
)

func TestGenerateIdentityFormat(t *testing.T) {
	for i := 0; i < 50; i++ {
		id, err := GenerateIdentity()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		parts := strings.Split(id, "-")
		if len(parts) != 3 {
			t.Fatalf("expected adjective-animal-hex form, got %q", id)
		}
		if len(parts[2]) != 4 {
			t.Fatalf("hex suffix should be 4 chars, got %q", parts[2])
		}
		if !containsIn(Adjectives, parts[0]) {
			t.Fatalf("unknown adjective %q in %q", parts[0], id)
		}
		if !containsIn(Animals, parts[1]) {
			t.Fatalf("unknown animal %q in %q", parts[1], id)
		}
	}
}

func TestGenerateIdentityIsRandomish(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, err := GenerateIdentity()
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("collision after %d iterations: %q", i, id)
		}
		seen[id] = true
	}
}

func containsIn(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
