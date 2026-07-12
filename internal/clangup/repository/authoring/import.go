package authoring

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func ImportBundle(workspace, descriptorPath string) (*ImportedRelease, error) {
	if _, err := LoadWorkspace(workspace); err != nil {
		return nil, err
	}
	descriptorPath, err := filepath.Abs(descriptorPath)
	if err != nil {
		return nil, err
	}
	bundleRoot := filepath.Dir(descriptorPath)
	var bundle Bundle
	if err := readJSON(descriptorPath, &bundle); err != nil {
		return nil, err
	}
	if bundle.Schema != "clangup.release-bundle/v1" {
		return nil, fmt.Errorf("unsupported release bundle schema %q", bundle.Schema)
	}
	identity := ReleaseIdentity{Channel: bundle.Channel, Version: bundle.Version, Release: bundle.Release}
	if !channelPattern.MatchString(identity.Channel) || identity.Version == "" || identity.Release < 1 {
		return nil, fmt.Errorf("invalid release identity: %#v", identity)
	}
	lockPath, err := safeBundlePath(bundleRoot, bundle.LockedSpec)
	if err != nil {
		return nil, err
	}
	lockDigest, _, err := sha256File(lockPath)
	if err != nil {
		return nil, err
	}
	if lockDigest != bundle.LockedSpecSHA256 {
		return nil, fmt.Errorf("locked spec sha256 mismatch")
	}
	var lock LockedSpec
	if err := readJSONLoose(lockPath, &lock); err != nil {
		return nil, err
	}
	if lock.Schema != "clangup.build-lock/v1" || lock.Release != identity {
		return nil, fmt.Errorf("locked spec release identity mismatch")
	}
	required := map[string]bool{}
	for _, target := range lock.Targets {
		if target.Required {
			required[target.Triple] = true
		}
	}

	release := &ImportedRelease{
		Schema: ReleaseSchema, Release: identity, LockedSpecSHA256: lockDigest,
	}
	for _, item := range lock.Changelog {
		if item.Release == identity.Release {
			release.Changelog = item.Summary
			date, err := time.Parse("2006-01-02", item.Date)
			if err != nil {
				return nil, fmt.Errorf("invalid changelog date for release %d: %w", identity.Release, err)
			}
			release.ReleasedAt = date.UTC().Format(time.RFC3339)
		}
	}
	if release.ReleasedAt == "" {
		return nil, fmt.Errorf("locked spec has no changelog date for release %d", identity.Release)
	}
	if err := copyObject(lockPath, workspace, lockDigest); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, artifact := range bundle.Artifacts {
		if !targetPattern.MatchString(artifact.Target) {
			return nil, fmt.Errorf("invalid artifact target %q", artifact.Target)
		}
		if seen[artifact.Target] {
			return nil, fmt.Errorf("duplicate artifact target %q", artifact.Target)
		}
		seen[artifact.Target] = true
		manifestPath, err := safeBundlePath(bundleRoot, artifact.Manifest)
		if err != nil {
			return nil, err
		}
		payloadPath, err := safeBundlePath(bundleRoot, artifact.Payload)
		if err != nil {
			return nil, err
		}
		buildRecordPath, err := safeBundlePath(bundleRoot, artifact.BuildRecord)
		if err != nil {
			return nil, err
		}
		var manifest ArtifactManifest
		if err := readJSONLoose(manifestPath, &manifest); err != nil {
			return nil, err
		}
		if manifest.Schema != "clangup.artifact/v1" || manifest.Release != identity || manifest.RuntimeRequirements.Triple != artifact.Target {
			return nil, fmt.Errorf("manifest identity mismatch for target %q", artifact.Target)
		}
		manifestDigest, _, err := sha256File(manifestPath)
		if err != nil || manifestDigest != artifact.ManifestSHA256 {
			return nil, fmt.Errorf("manifest sha256 mismatch for target %q", artifact.Target)
		}
		payloadDigest, payloadSize, err := sha256File(payloadPath)
		if err != nil || payloadDigest != artifact.PayloadSHA256 || payloadDigest != manifest.Artifact.SHA256 || payloadSize != manifest.Artifact.Size {
			return nil, fmt.Errorf("payload identity mismatch for target %q", artifact.Target)
		}
		if filepath.Base(artifact.Payload) != manifest.Artifact.Name {
			return nil, fmt.Errorf("payload name mismatch for target %q", artifact.Target)
		}
		if manifest.Source.Archive.SHA256 != lock.Source.SHA256 || manifest.Source.PatchsetSHA256 != lock.Source.PatchsetSHA256 {
			return nil, fmt.Errorf("manifest source identity mismatch for target %q", artifact.Target)
		}
		expectedSourceTarget := fmt.Sprintf("sources/sha256/%s.tar.xz", lock.Source.SHA256)
		if manifest.Source.Archive.Target != expectedSourceTarget {
			return nil, fmt.Errorf("manifest source target mismatch for target %q", artifact.Target)
		}
		if len(manifest.Source.Patches) != len(lock.Source.Patches) {
			return nil, fmt.Errorf("manifest patch count mismatch for target %q", artifact.Target)
		}
		for index, patch := range lock.Source.Patches {
			expectedPatchTarget := fmt.Sprintf("patches/sha256/%s.patch", patch.SHA256)
			if manifest.Source.Patches[index].SHA256 != patch.SHA256 || manifest.Source.Patches[index].Target != expectedPatchTarget {
				return nil, fmt.Errorf("manifest patch identity mismatch for target %q", artifact.Target)
			}
		}
		var buildRecord BuildRecord
		if err := readJSONLoose(buildRecordPath, &buildRecord); err != nil {
			return nil, err
		}
		if buildRecord.Schema != "clangup.build-record/v1" || buildRecord.Release != identity || buildRecord.Target != artifact.Target || buildRecord.LockedSpecSHA256 != lockDigest || buildRecord.ArtifactSHA256 != payloadDigest {
			return nil, fmt.Errorf("build record identity mismatch for target %q", artifact.Target)
		}
		buildRecordDigest, _, err := sha256File(buildRecordPath)
		if err != nil || buildRecordDigest != artifact.BuildRecordSHA256 {
			return nil, fmt.Errorf("build record sha256 mismatch for target %q", artifact.Target)
		}
		for path, digest := range map[string]string{manifestPath: manifestDigest, payloadPath: payloadDigest, buildRecordPath: buildRecordDigest} {
			if err := copyObject(path, workspace, digest); err != nil {
				return nil, err
			}
		}
		release.Artifacts = append(release.Artifacts, ImportedArtifact{
			Target: artifact.Target, Name: manifest.Artifact.Name, PayloadSHA256: payloadDigest,
			ManifestSHA256: manifestDigest, BuildRecordSHA256: buildRecordDigest,
		})
	}
	for target := range required {
		if !seen[target] {
			return nil, fmt.Errorf("required target is missing: %s", target)
		}
	}
	for _, object := range bundle.Objects {
		path, err := safeBundlePath(bundleRoot, object.Path)
		if err != nil {
			return nil, err
		}
		if object.Kind != "source" && object.Kind != "patch" {
			return nil, fmt.Errorf("unsupported bundle object kind %q", object.Kind)
		}
		if err := copyObject(path, workspace, object.SHA256); err != nil {
			return nil, err
		}
		release.Objects = append(release.Objects, ImportedObject{Kind: object.Kind, SHA256: object.SHA256, Name: filepath.Base(object.Path)})
	}
	if err := validateSourceObjects(lock, release.Objects); err != nil {
		return nil, err
	}
	sort.Slice(release.Artifacts, func(i, j int) bool { return release.Artifacts[i].Target < release.Artifacts[j].Target })
	sort.Slice(release.Objects, func(i, j int) bool {
		if release.Objects[i].Kind != release.Objects[j].Kind {
			return release.Objects[i].Kind < release.Objects[j].Kind
		}
		return release.Objects[i].SHA256 < release.Objects[j].SHA256
	})
	releaseDirectory := filepath.Join(workspace, "releases", identity.Channel, fmt.Sprintf("%s-%d", identity.Version, identity.Release))
	releasePath := filepath.Join(releaseDirectory, "release.toml")
	contents, err := encodeTOML(release)
	if err != nil {
		return nil, err
	}
	if existing, err := os.ReadFile(releasePath); err == nil {
		if string(existing) != string(contents) {
			return nil, fmt.Errorf("release already exists with different content: %s", releasePath)
		}
		return release, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := writeAtomic(releasePath, contents, 0o644); err != nil {
		return nil, err
	}
	for _, artifact := range release.Artifacts {
		if err := copyFile(filepath.Join(workspace, "objects", "sha256", artifact.ManifestSHA256), filepath.Join(releaseDirectory, "manifests", artifact.Target+".json")); err != nil {
			return nil, err
		}
	}
	return release, nil
}

func readJSONLoose(path string, value any) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(contents, value); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func validateSourceObjects(lock LockedSpec, objects []ImportedObject) error {
	found := map[string]string{}
	for _, object := range objects {
		found[object.SHA256] = object.Kind
	}
	if found[lock.Source.SHA256] != "source" {
		return fmt.Errorf("bundle does not contain locked source object")
	}
	for _, patch := range lock.Source.Patches {
		if found[patch.SHA256] != "patch" {
			return fmt.Errorf("bundle does not contain locked patch %s", patch.SHA256)
		}
	}
	return nil
}
