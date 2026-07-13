package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type InstallRecord struct {
	Schema             string         `json:"schema"`
	Channel            string         `json:"channel"`
	Version            string         `json:"version"`
	Release            int            `json:"release"`
	Target             string         `json:"target"`
	Prefix             string         `json:"prefix"`
	ManifestSHA256     string         `json:"manifest_sha256"`
	ArtifactSHA256     string         `json:"artifact_sha256"`
	DriverRequirements []string       `json:"driver_requirements,omitempty"`
	ArchiveSHA256      string         `json:"archive_sha256,omitempty"`
	PatchsetSHA256     string         `json:"patchset_sha256,omitempty"`
	Driver             map[string]any `json:"driver,omitempty"`
	Optimization       map[string]any `json:"optimization,omitempty"`
}

func (record InstallRecord) Exact() string {
	return fmt.Sprintf("%s-%d", record.Version, record.Release)
}
func (record InstallRecord) ID() string {
	return fmt.Sprintf("%s@%s#%s", record.Channel, record.Exact(), record.Target)
}

func IsInstalled(prefix, manifestSHA256, artifactSHA256 string) bool {
	record, err := LoadInstallRecord(prefix)
	if err != nil || record.Prefix != prefix || record.ManifestSHA256 != manifestSHA256 || record.ArtifactSHA256 != artifactSHA256 {
		return false
	}
	for _, executable := range []string{"bin/clang", "bin/clang++"} {
		info, err := os.Stat(filepath.Join(prefix, executable))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func RecordInstall(record InstallRecord) error {
	if record.Channel == "" || record.Version == "" || record.Release < 1 || record.Target == "" || record.Prefix == "" {
		return fmt.Errorf("incomplete install record")
	}
	record.Schema = "clangup.install-record/v2"
	path, err := installRecordPath(record.Prefix)
	if err != nil {
		return err
	}
	contents, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(contents, '\n'))
}

func LoadInstallRecord(prefix string) (*InstallRecord, error) {
	path, err := installRecordPath(prefix)
	if err != nil {
		return nil, err
	}
	return loadInstallRecordPath(path)
}

func ListInstalls() ([]InstallRecord, error) {
	root, err := DataRoot()
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(root, "state", "installs")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]InstallRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		record, err := loadInstallRecordPath(filepath.Join(directory, entry.Name()))
		if err != nil || record.Schema != "clangup.install-record/v2" {
			continue
		}
		result = append(result, *record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID() < result[j].ID() })
	return result, nil
}

func RemoveInstallRecord(prefix string) error {
	path, err := installRecordPath(prefix)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func loadInstallRecordPath(path string) (*InstallRecord, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record InstallRecord
	if err := json.Unmarshal(contents, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func installRecordPath(prefix string) (string, error) {
	root, err := DataRoot()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(prefix))
	return filepath.Join(root, "state", "installs", hex.EncodeToString(digest[:])+".json"), nil
}
