package identity

import (
	"regexp"
	"testing"
)

func TestGenerate(t *testing.T) {
	name, err := Generate()
	if err != nil {
		t.Fatalf("Generate() failed: %v", err)
	}

	match, _ := regexp.MatchString("^[a-z]+-[a-z]+-[0-9a-f]{4}$", name)
	if !match {
		t.Errorf("name %q does not match expected pattern", name)
	}
}
