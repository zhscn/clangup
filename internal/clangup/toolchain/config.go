package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func ConfigRoot() (string, error) {
	if root := os.Getenv("CLANGUP_CONFIG_HOME"); root != "" {
		return filepath.Abs(root)
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "clangup"), nil
}

func ConfigPath() (string, error) {
	root, err := ConfigRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "repositories.json"), nil
}

func CatalogPath(repository Repository) (string, error) {
	root, err := ConfigRoot()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(repository.URL))
	return filepath.Join(root, "repositories", hex.EncodeToString(digest[:]), "catalog-v1.json"), nil
}

func RemoveCatalog(repository Repository) error {
	path, err := CatalogPath(repository)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		return err
	}
	return nil
}

func CacheRoot() (string, error) {
	if root := os.Getenv("CLANGUP_CACHE_HOME"); root != "" {
		return filepath.Abs(root)
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "clangup"), nil
}

func DataRoot() (string, error) {
	if root := os.Getenv("CLANGUP_HOME"); root != "" {
		return filepath.Abs(root)
	}
	if root := os.Getenv("XDG_DATA_HOME"); root != "" {
		return filepath.Join(root, "clangup"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "clangup"), nil
}

func LoadConfig() (*RepositoryConfig, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &RepositoryConfig{Schema: "clangup.config/v1"}, nil
	}
	if err != nil {
		return nil, err
	}
	var config RepositoryConfig
	if err := json.Unmarshal(contents, &config); err != nil {
		return nil, err
	}
	if config.Schema != "clangup.config/v1" {
		return nil, fmt.Errorf("unsupported config schema %q", config.Schema)
	}
	return &config, nil
}

func SaveConfig(config *RepositoryConfig) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	contents, err := json.Marshal(config)
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".repositories-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
