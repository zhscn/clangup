package authoring

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

var (
	channelPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	targetPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	namespacePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?:/[A-Za-z0-9._~-]+)*$`)
	digestPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func Init(path, namespace, displayName string, localKeys bool) error {
	if !namespacePattern.MatchString(namespace) || !strings.Contains(strings.Split(namespace, "/")[0], ".") {
		return fmt.Errorf("invalid repository namespace %q", namespace)
	}
	if displayName == "" {
		displayName = namespace
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) != 0 {
		return fmt.Errorf("workspace directory is not empty: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, directory := range []string{"channels", "releases", "objects/sha256", "state"} {
		if err := os.MkdirAll(filepath.Join(path, directory), 0o755); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(filepath.Join(path, "repository.toml"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	config := Workspace{Schema: WorkspaceSchema, Namespace: namespace, DisplayName: displayName}
	encodeErr := toml.NewEncoder(file).Encode(config)
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.WriteFile(filepath.Join(path, ".gitignore"), []byte("/state/keys/\n"), 0o644); err != nil {
		return err
	}
	if localKeys {
		if err := generateLocalKeys(filepath.Join(path, "state", "keys")); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(path, "state", "keys", "README"), []byte("Development-only unencrypted Ed25519 keys. Do not commit or use for production.\n"), 0o600)
	}
	return nil
}

func LoadWorkspace(path string) (*Workspace, error) {
	var workspace Workspace
	metadata, err := toml.DecodeFile(filepath.Join(path, "repository.toml"), &workspace)
	if err != nil {
		return nil, err
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		return nil, fmt.Errorf("repository.toml has unknown fields: %v", undecoded)
	}
	if workspace.Schema != WorkspaceSchema {
		return nil, fmt.Errorf("unsupported workspace schema %q", workspace.Schema)
	}
	return &workspace, nil
}

func readTOML(path string, value any) error {
	metadata, err := toml.DecodeFile(path, value)
	if err != nil {
		return err
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		return fmt.Errorf("%s has unknown fields: %v", path, undecoded)
	}
	return nil
}

func encodeTOML(value any) ([]byte, error) {
	var output strings.Builder
	if err := toml.NewEncoder(&output).Encode(value); err != nil {
		return nil, err
	}
	return []byte(output.String()), nil
}

func writeTOML(path string, value any) error {
	contents, err := encodeTOML(value)
	if err != nil {
		return err
	}
	return writeAtomic(path, contents, 0o644)
}

func readJSON(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("decode %s: trailing JSON data", path)
	}
	return nil
}

func writeCanonical(path string, value any, mode os.FileMode) error {
	contents, err := json.Marshal(value)
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	return writeAtomic(path, contents, mode)
}

func writeAtomic(path string, contents []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".clangup-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func sha256File(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(digest.Sum(nil)), size, nil
}

func safeBundlePath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe bundle path %q", relative)
	}
	path := filepath.Join(root, relative)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absoluteResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if absolutePath != absoluteResolved {
		return "", fmt.Errorf("bundle path contains a symlink: %s", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("bundle path is not a regular file: %s", path)
	}
	return path, nil
}

func copyObject(source, workspace, digest string) error {
	if !digestPattern.MatchString(digest) {
		return fmt.Errorf("invalid sha256 %q", digest)
	}
	actual, _, err := sha256File(source)
	if err != nil {
		return err
	}
	if actual != digest {
		return fmt.Errorf("sha256 mismatch for %s: expected %s, got %s", source, digest, actual)
	}
	destination := filepath.Join(workspace, "objects", "sha256", digest)
	if existing, _, err := sha256File(destination); err == nil {
		if existing != digest {
			return fmt.Errorf("corrupt object already exists: %s", destination)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".object-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, input); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}
