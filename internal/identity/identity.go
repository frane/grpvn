package identity

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

func Generate() (string, error) {
	adjIdx, err := rand.Int(rand.Reader, big.NewInt(int64(len(adjectives))))
	if err != nil {
		return "", err
	}
	animIdx, err := rand.Int(rand.Reader, big.NewInt(int64(len(animals))))
	if err != nil {
		return "", err
	}

	suffix := make([]byte, 2)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}

	name := fmt.Sprintf("%s-%s-%s", adjectives[adjIdx.Int64()], animals[animIdx.Int64()], hex.EncodeToString(suffix))
	return name, nil
}
