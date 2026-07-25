package clangup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

// The installed-toolchain side: finding, listing, removing, and garbage
// collecting what is on disk.

func findInstalled(selector string) (*toolchain.InstallRecord, error) {
	records, err := toolchain.ListInstalls()
	if err != nil {
		return nil, err
	}
	var matches []toolchain.InstallRecord
	base, exact, _ := strings.Cut(selector, "@")
	for _, record := range records {
		if selector == record.ID() || selector == record.Prefix {
			copy := record
			return &copy, nil
		}
		matched := record.Channel == base || record.Version == base || record.Exact() == base
		if matched && (exact == "" || exact == record.Exact()) {
			matches = append(matches, record)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("installed toolchain not found: %s", selector)
	}
	if len(matches) > 1 {
		ids := make([]string, len(matches))
		for index := range matches {
			ids[index] = matches[index].ID()
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("installed toolchain is ambiguous: %s (%s)", selector, strings.Join(ids, ", "))
	}
	return &matches[0], nil
}

func removeMissingInstallRecords() error {
	records, err := toolchain.ListInstalls()
	if err != nil {
		return err
	}
	for _, record := range records {
		if _, err := os.Stat(record.Prefix); errors.Is(err, fs.ErrNotExist) {
			_ = toolchain.RemoveInstallRecord(record.Prefix)
		}
	}
	return nil
}

// installedToolchains lists every install record together with the prefix
// currently marked default.
func installedToolchains() (records []toolchain.InstallRecord, defaultPrefix string, err error) {
	records, err = toolchain.ListInstalls()
	if err != nil {
		return nil, "", err
	}
	current, err := toolchain.LoadDefault()
	if err != nil {
		return nil, "", err
	}
	return records, current.Prefix, nil
}

// removeToolchain deletes an installed toolchain's tree and its install
// record, unpinning the default first when it pointed there.
func removeToolchain(record *toolchain.InstallRecord) error {
	current, err := toolchain.LoadDefault()
	if err != nil {
		return err
	}
	if current.Prefix == record.Prefix {
		if err := toolchain.ClearDefault(); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(record.Prefix); err != nil {
		return err
	}
	return toolchain.RemoveInstallRecord(record.Prefix)
}

// collectGarbage removes what an interrupted download or install left
// behind — .partial objects and .clangup-install-* staging trees — plus
// install records whose prefix is gone. It returns the paths removed.
func collectGarbage() ([]string, error) {
	cache, err := toolchain.CacheRoot()
	if err != nil {
		return nil, err
	}
	data, err := toolchain.DataRoot()
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, root := range []string{cache, filepath.Join(data, "toolchains")} {
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || path == root {
				return nil
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".partial") && !(entry.IsDir() && strings.HasPrefix(name, ".clangup-install-")) {
				return nil
			}
			if os.RemoveAll(path) == nil {
				removed = append(removed, path)
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		})
	}
	return removed, removeMissingInstallRecords()
}
