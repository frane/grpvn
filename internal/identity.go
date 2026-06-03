package internal

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

func GenerateIdentity() (string, error) {
	adjIdx, err := rand.Int(rand.Reader, big.NewInt(int64(len(Adjectives))))
	if err != nil {
		return "", err
	}
	animIdx, err := rand.Int(rand.Reader, big.NewInt(int64(len(Animals))))
	if err != nil {
		return "", err
	}
	suffix := make([]byte, 2)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%s", Adjectives[adjIdx.Int64()], Animals[animIdx.Int64()], hex.EncodeToString(suffix)), nil
}
