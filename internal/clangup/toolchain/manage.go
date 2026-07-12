package toolchain

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type DefaultState struct {
	Schema string   `json:"schema"`
	Prefix string   `json:"prefix"`
	Links  []string `json:"links"`
}

func BinRoot() (string, error) {
	root, err := DataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "bin"), nil
}

func LoadDefault() (*DefaultState, error) {
	path, err := defaultStatePath()
	if err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &DefaultState{Schema: "clangup.default/v1"}, nil
	}
	if err != nil {
		return nil, err
	}
	var state DefaultState
	if err := json.Unmarshal(contents, &state); err != nil {
		return nil, err
	}
	if state.Schema != "clangup.default/v1" {
		return nil, errors.New("unsupported default toolchain state")
	}
	return &state, nil
}

func SetDefault(prefix string) error {
	state, err := LoadDefault()
	if err != nil {
		return err
	}
	bin, err := BinRoot()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(prefix, "bin"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	links := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || entry.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		links = append(links, entry.Name())
	}
	managed := make(map[string]bool, len(state.Links))
	for _, name := range state.Links {
		managed[name] = true
	}
	for _, name := range links {
		destination := filepath.Join(bin, name)
		if _, err := os.Lstat(destination); err == nil && !managed[name] {
			return errors.New("default toolchain link conflicts with " + destination)
		} else if !errors.Is(err, fs.ErrNotExist) {
			if !managed[name] {
				return err
			}
		}
	}
	for _, name := range state.Links {
		path := filepath.Join(bin, name)
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	for _, name := range links {
		destination := filepath.Join(bin, name)
		target, err := filepath.Rel(bin, filepath.Join(prefix, "bin", name))
		if err != nil {
			return err
		}
		if err := os.Symlink(target, destination); err != nil {
			return err
		}
	}
	contents, err := json.Marshal(DefaultState{Schema: "clangup.default/v1", Prefix: prefix, Links: links})
	if err != nil {
		return err
	}
	path, err := defaultStatePath()
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(contents, '\n'))
}

func ClearDefault() error {
	state, err := LoadDefault()
	if err != nil {
		return err
	}
	bin, err := BinRoot()
	if err != nil {
		return err
	}
	for _, name := range state.Links {
		path := filepath.Join(bin, name)
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	path, err := defaultStatePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func defaultStatePath() (string, error) {
	root, err := DataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "state", "default.json"), nil
}
