package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

func Load(path string) (*Loaded, error) {
	if filepath.Ext(path) != ".yaml" {
		return nil, fmt.Errorf("authoring spec must use the .yaml extension")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve spec path: %w", err)
	}
	if err := requireRegularFile(absolutePath); err != nil {
		return nil, fmt.Errorf("spec: %w", err)
	}

	var authoring Spec
	if err := decodeYAMLFile(absolutePath, &authoring); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	loaded := &Loaded{
		Path:       absolutePath,
		BundleRoot: filepath.Dir(absolutePath),
		Spec:       authoring,
	}
	if err := validateSpec(&loaded.Spec); err != nil {
		return nil, err
	}
	if authoring.Source.PatchSeries != "" {
		patches, err := loadPatchSeries(loaded.BundleRoot, authoring.Source.PatchSeries)
		if err != nil {
			return nil, err
		}
		loaded.Patches = patches
	}
	return loaded, nil
}

func loadPatchSeries(bundleRoot, relativePath string) ([]LockedPatch, error) {
	if filepath.Ext(relativePath) != ".yaml" {
		return nil, fmt.Errorf("patch series must use the .yaml extension")
	}
	seriesPath, err := resolveBundleFile(bundleRoot, relativePath)
	if err != nil {
		return nil, fmt.Errorf("patch series: %w", err)
	}
	var series PatchSeries
	if err := decodeYAMLFile(seriesPath, &series); err != nil {
		return nil, fmt.Errorf("decode patch series %s: %w", relativePath, err)
	}
	if series.Schema != "clangup.patch-series/v1" {
		return nil, fmt.Errorf("patch series schema must be %q", "clangup.patch-series/v1")
	}
	if series.Strip < 0 || series.Strip > 10 {
		return nil, fmt.Errorf("patch series strip must be between 0 and 10")
	}
	if len(series.Patches) == 0 {
		return nil, fmt.Errorf("patch series must contain at least one patch")
	}

	seen := make(map[string]struct{}, len(series.Patches))
	patches := make([]LockedPatch, 0, len(series.Patches))
	for index, entry := range series.Patches {
		if _, ok := seen[entry.Path]; ok {
			return nil, fmt.Errorf("patch series entry %d repeats path %q", index, entry.Path)
		}
		seen[entry.Path] = struct{}{}
		if err := validateSHA256(entry.SHA256, fmt.Sprintf("patch %q sha256", entry.Path)); err != nil {
			return nil, err
		}
		patchPath, err := resolveBundleFile(bundleRoot, entry.Path)
		if err != nil {
			return nil, fmt.Errorf("patch %q: %w", entry.Path, err)
		}
		digest, err := hashFile(patchPath)
		if err != nil {
			return nil, fmt.Errorf("hash patch %q: %w", entry.Path, err)
		}
		if digest != entry.SHA256 {
			return nil, fmt.Errorf("patch %q sha256 mismatch: expected %s, got %s", entry.Path, entry.SHA256, digest)
		}
		patches = append(patches, LockedPatch{Path: filepath.ToSlash(entry.Path), SHA256: digest, Strip: series.Strip})
	}
	return patches, nil
}

func decodeYAMLFile(path string, value any) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return err
	}
	if err := validateYAMLNode(&document); err != nil {
		return err
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(contents)))
	decoder.KnownFields(true)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple YAML documents are not allowed")
		}
		return err
	}
	return nil
}

func validateYAMLNode(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return fmt.Errorf("YAML anchors and aliases are not allowed")
	}
	if node.Tag != "" && !strings.HasPrefix(node.Tag, "!!") &&
		!strings.HasPrefix(node.Tag, "tag:yaml.org,2002:") {
		return fmt.Errorf("custom YAML tag %q is not allowed", node.Tag)
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("YAML mapping keys must be strings")
			}
			if _, ok := seen[key.Value]; ok {
				return fmt.Errorf("duplicate YAML key %q", key.Value)
			}
			seen[key.Value] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := validateYAMLNode(child); err != nil {
			return err
		}
	}
	return nil
}

func resolveBundleFile(bundleRoot, relativePath string) (string, error) {
	if relativePath == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.Contains(relativePath, "\\") {
		return "", fmt.Errorf("path %q contains a backslash", relativePath)
	}
	if filepath.IsAbs(relativePath) || filepath.Clean(relativePath) != relativePath || relativePath == "." {
		return "", fmt.Errorf("path %q is not a clean bundle-relative path", relativePath)
	}
	for _, part := range strings.Split(filepath.ToSlash(relativePath), "/") {
		if part == ".." || part == "" {
			return "", fmt.Errorf("path %q escapes the bundle", relativePath)
		}
	}

	current := bundleRoot
	for _, part := range strings.Split(filepath.ToSlash(relativePath), "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path %q contains symlink component %q", relativePath, part)
		}
	}
	if err := requireRegularFile(current); err != nil {
		return "", err
	}
	return current, nil
}

func requireRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
