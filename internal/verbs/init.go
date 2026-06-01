package verbs

import (
	"fmt"
	"os"

	"grpvn/internal/identity"
	"grpvn/internal/state"
)

func Init(statePath string, asName string, force bool) (string, error) {
	if _, err := os.Stat(statePath); err == nil && !force {
		return "", fmt.Errorf("state file already exists at %s (use --force to overwrite)", statePath)
	}

	name := asName
	if name == "" {
		var err error
		name, err = identity.Generate()
		if err != nil {
			return "", fmt.Errorf("generate identity: %w", err)
		}
	}

	s := &state.State{
		Name:   name,
		Follow: []string{},
	}

	if err := s.Save(statePath); err != nil {
		return "", fmt.Errorf("save state: %w", err)
	}

	return name, nil
}
