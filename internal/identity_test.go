package internal

import (
	"path/filepath"
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

// The pools carry the diversity guarantee: big enough that a host full of
// agents rarely repeats a word, unique within each list, and disjoint
// across the two lists so "wild-wild-1a2b" can't be minted.
func TestWordPoolInvariants(t *testing.T) {
	for name, pool := range map[string][]string{"Adjectives": Adjectives, "Animals": Animals} {
		if len(pool) < 200 {
			t.Fatalf("%s pool too small for diversity: %d", name, len(pool))
		}
		seen := map[string]bool{}
		for _, w := range pool {
			if w == "" || strings.ToLower(w) != w || strings.ContainsAny(w, "-0123456789 ") {
				t.Fatalf("%s: bad word %q", name, w)
			}
			if seen[w] {
				t.Fatalf("%s: duplicate word %q", name, w)
			}
			seen[w] = true
		}
	}
	adj := map[string]bool{}
	for _, w := range Adjectives {
		adj[w] = true
	}
	for _, w := range Animals {
		if adj[w] {
			t.Fatalf("word %q appears in both pools", w)
		}
	}
}

// A new identity must not share a word with any identity this host can
// already see — state files or DB senders.
func TestGenerateIdentityAvoidsHostWords(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	// Pin the state path: an ambient GRPVN_STATE (the installer sets it
	// session-wide) would point hostUsedWords at the real ~/.grpvn.
	t.Setenv("GRPVN_STATE", filepath.Join(home, ".grpvn", "state.json"))
	db := newTestDB(t) // points GRPVN_DB at a temp store
	NewMessage("gold-moth-34c0", "#dev", []byte("hi")).Save(db)
	st := &State{Name: "wild-lynx-22eb"}
	if err := st.Save(filepath.Join(home, ".grpvn", "state.json")); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 25; i++ {
		id, err := GenerateIdentity()
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.SplitN(id, "-", 3)
		for _, w := range []string{"gold", "moth", "wild", "lynx"} {
			if parts[0] == w || parts[1] == w {
				t.Fatalf("minted %q reuses host word %q", id, w)
			}
		}
	}
}
