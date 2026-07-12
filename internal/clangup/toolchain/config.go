package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
)

const OfficialIndexURL = "https://dl.clangup.dev/index.json"

func IndexURL() string {
	if value := os.Getenv("CLANGUP_INDEX_URL"); value != "" {
		return value
	}
	return OfficialIndexURL
}

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

func IndexPath() (string, error) {
	root, err := ConfigRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "index.json"), nil
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

func ValidateIndex(index *Index) error {
	if index.Schema != "clangup.index/v1" || index.DefaultChannel == "" || len(index.Channels) == 0 {
		return fmt.Errorf("invalid clangup index")
	}
	if _, ok := index.Channels[index.DefaultChannel]; !ok {
		return fmt.Errorf("default channel is absent from index")
	}
	return nil
}
