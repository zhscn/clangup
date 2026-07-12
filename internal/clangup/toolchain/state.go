package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

type InstallRecord struct {
	Schema         string `json:"schema"`
	Prefix         string `json:"prefix"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ArtifactSHA256 string `json:"artifact_sha256"`
}

func IsInstalled(prefix, manifestSHA256, artifactSHA256 string) bool {
	record, err := loadInstallRecord(prefix)
	if err != nil || record.Schema != "clangup.install-record/v1" || record.Prefix != prefix || record.ManifestSHA256 != manifestSHA256 || record.ArtifactSHA256 != artifactSHA256 {
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

func RecordInstall(prefix, manifestSHA256, artifactSHA256 string) error {
	path, err := installRecordPath(prefix)
	if err != nil {
		return err
	}
	record := InstallRecord{
		Schema: "clangup.install-record/v1", Prefix: prefix,
		ManifestSHA256: manifestSHA256, ArtifactSHA256: artifactSHA256,
	}
	contents, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(contents, '\n'))
}

func loadInstallRecord(prefix string) (*InstallRecord, error) {
	path, err := installRecordPath(prefix)
	if err != nil {
		return nil, err
	}
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
