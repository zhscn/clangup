package authoring

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type localKey struct {
	Schema  string `json:"schema"`
	Role    string `json:"role"`
	Private string `json:"private"`
}

func generateLocalKeys(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		key := localKey{Schema: "clangup.local-key/v1", Role: role, Private: hex.EncodeToString(private)}
		if err := writeCanonical(filepath.Join(directory, role+".json"), key, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func loadLocalKey(path, role string) (ed25519.PrivateKey, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var key localKey
	if err := json.Unmarshal(contents, &key); err != nil {
		return nil, err
	}
	if key.Schema != "clangup.local-key/v1" || key.Role != role {
		return nil, fmt.Errorf("invalid local %s key", role)
	}
	private, err := hex.DecodeString(key.Private)
	if err != nil || len(private) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid local %s private key", role)
	}
	return ed25519.PrivateKey(private), nil
}
