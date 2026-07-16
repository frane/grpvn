package internal

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

// GenerateIdentity mints an adjective-animal-hex name whose adjective AND
// animal are both unused by any identity visible on this host, so two
// runtimes never read as siblings ("wild-lynx" next to "green-lynx").
// Visibility is best-effort — state files next to the active one plus the
// distinct senders in the store, and any error there just means minting
// without the distinctness guarantee. The hex suffix carries global
// uniqueness either way; word distinctness is purely for humans. After 40
// attempts (a host would need dozens of identities to get there) the last
// candidate wins.
func GenerateIdentity() (string, error) {
	used := hostUsedWords()
	var name string
	for i := 0; i < 40; i++ {
		var err error
		name, err = randomName()
		if err != nil {
			return "", err
		}
		parts := strings.SplitN(name, "-", 3)
		if !used[parts[0]] && !used[parts[1]] {
			return name, nil
		}
	}
	return name, nil
}

func randomName() (string, error) {
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

// hostUsedWords collects every word already spent on an identity this host
// can see: the state*.json files in the active state directory, and the
// distinct senders in the store — but only if the store file already
// exists, because minting an identity must not create a database as a side
// effect. All failures degrade to "word not known used".
func hostUsedWords() map[string]bool {
	used := map[string]bool{}
	note := func(name string) {
		parts := strings.SplitN(name, "-", 3)
		if len(parts) >= 2 {
			used[parts[0]] = true
			used[parts[1]] = true
		}
	}
	dir := filepath.Dir(ResolveStatePath(""))
	if paths, err := filepath.Glob(filepath.Join(dir, "state*.json")); err == nil {
		for _, p := range paths {
			if st, err := LoadState(p); err == nil && st.Name != "" {
				note(st.Name)
			}
		}
	}
	path, err := dbPath()
	if err != nil {
		return used
	}
	if _, err := os.Stat(path); err != nil {
		return used
	}
	db, err := OpenDB()
	if err != nil {
		return used
	}
	defer db.Close()
	rows, err := db.Query("SELECT DISTINCT sender FROM messages")
	if err != nil {
		return used
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			note(s)
		}
	}
	return used
}
