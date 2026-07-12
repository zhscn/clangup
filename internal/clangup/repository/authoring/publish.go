package authoring

import (
	"crypto"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

type catalog struct {
	Schema     string                    `json:"schema"`
	Repository catalogRepository         `json:"repository"`
	Channels   map[string]catalogChannel `json:"channels"`
}

type catalogRepository struct {
	Namespace      string `json:"namespace"`
	DisplayName    string `json:"display_name"`
	DefaultChannel string `json:"default_channel,omitempty"`
}

type catalogChannel struct {
	Description string           `json:"description,omitempty"`
	Current     string           `json:"current"`
	Releases    []catalogRelease `json:"releases"`
}

type catalogRelease struct {
	Version    string            `json:"version"`
	Release    int               `json:"release"`
	ReleasedAt string            `json:"released_at"`
	Changelog  string            `json:"changelog,omitempty"`
	Artifacts  []catalogArtifact `json:"artifacts"`
}

type catalogArtifact struct {
	Target   string `json:"target"`
	Manifest string `json:"manifest"`
}

type targetSource struct {
	Object string
	Bytes  []byte
}

func PublishFilesystem(workspace, output string, expiresFrom time.Time) error {
	config, err := LoadWorkspace(workspace)
	if err != nil {
		return err
	}
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("repository output already exists: %s", output)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(output), ".clangup-repository-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	metadataDirectory := filepath.Join(temporary, "metadata")
	targetsDirectory := filepath.Join(temporary, "targets")
	if err := os.MkdirAll(metadataDirectory, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(targetsDirectory, 0o755); err != nil {
		return err
	}

	targets, catalogValue, err := collectTargets(workspace, config)
	if err != nil {
		return err
	}
	catalogBytes, err := json.Marshal(catalogValue)
	if err != nil {
		return err
	}
	catalogBytes = append(catalogBytes, '\n')
	targets["catalog-v1.json"] = targetSource{Bytes: catalogBytes}

	targetMetadata := metadata.Targets(expiresFrom.Add(180 * 24 * time.Hour))
	paths := make([]string, 0, len(targets))
	for path := range targets {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, logical := range paths {
		source := targets[logical]
		staged := filepath.Join(temporary, "staging", filepath.FromSlash(logical))
		if source.Object != "" {
			objectPath := filepath.Join(workspace, "objects", "sha256", source.Object)
			actual, _, err := sha256File(objectPath)
			if err != nil {
				return err
			}
			if actual != source.Object {
				return fmt.Errorf("workspace object is corrupt: %s", objectPath)
			}
			if err := copyFile(objectPath, staged); err != nil {
				return err
			}
		} else if err := writeAtomic(staged, source.Bytes, 0o644); err != nil {
			return err
		}
		info, err := metadata.TargetFile().FromFile(staged, "sha256")
		if err != nil {
			return err
		}
		targetMetadata.Signed.Targets[logical] = info
		digest := hex.EncodeToString(info.Hashes["sha256"])
		physical := consistentTargetPath(targetsDirectory, logical, digest)
		if err := copyFile(staged, physical); err != nil {
			return err
		}
	}

	root := metadata.Root(expiresFrom.Add(2 * 365 * 24 * time.Hour))
	root.Signed.ConsistentSnapshot = true
	snapshot := metadata.Snapshot(expiresFrom.Add(7 * 24 * time.Hour))
	timestamp := metadata.Timestamp(expiresFrom.Add(24 * time.Hour))
	roles := map[string]any{"root": root, "targets": targetMetadata, "snapshot": snapshot, "timestamp": timestamp}
	privateKeys := map[string]ed25519.PrivateKey{}
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		private, err := loadLocalKey(filepath.Join(workspace, "state", "keys", role+".json"), role)
		if err != nil {
			return fmt.Errorf("load development %s key: %w", role, err)
		}
		privateKeys[role] = private
		key, err := metadata.KeyFromPublicKey(private.Public())
		if err != nil {
			return err
		}
		if err := root.Signed.AddKey(key, role); err != nil {
			return err
		}
	}

	sign := func(role string, value any) error {
		signer, err := signature.LoadSigner(privateKeys[role], crypto.Hash(0))
		if err != nil {
			return err
		}
		switch meta := value.(type) {
		case *metadata.Metadata[metadata.RootType]:
			_, err = meta.Sign(signer)
		case *metadata.Metadata[metadata.TargetsType]:
			_, err = meta.Sign(signer)
		case *metadata.Metadata[metadata.SnapshotType]:
			_, err = meta.Sign(signer)
		case *metadata.Metadata[metadata.TimestampType]:
			_, err = meta.Sign(signer)
		default:
			return fmt.Errorf("unsupported metadata role %s", role)
		}
		return err
	}
	if err := sign("targets", roles["targets"]); err != nil {
		return err
	}
	targetsBytes, err := targetMetadata.ToBytes(false)
	if err != nil {
		return err
	}
	snapshot.Signed.Meta["targets.json"] = metaFile(1, targetsBytes)
	if err := sign("snapshot", roles["snapshot"]); err != nil {
		return err
	}
	snapshotBytes, err := snapshot.ToBytes(false)
	if err != nil {
		return err
	}
	timestamp.Signed.Meta["snapshot.json"] = metaFile(1, snapshotBytes)
	if err := sign("timestamp", roles["timestamp"]); err != nil {
		return err
	}
	if err := sign("root", roles["root"]); err != nil {
		return err
	}

	if err := root.VerifyDelegate("root", root); err != nil {
		return err
	}
	if err := root.VerifyDelegate("targets", targetMetadata); err != nil {
		return err
	}
	if err := root.VerifyDelegate("snapshot", snapshot); err != nil {
		return err
	}
	if err := root.VerifyDelegate("timestamp", timestamp); err != nil {
		return err
	}
	if err := root.ToFile(filepath.Join(metadataDirectory, "1.root.json"), false); err != nil {
		return err
	}
	if err := root.ToFile(filepath.Join(metadataDirectory, "root.json"), false); err != nil {
		return err
	}
	if err := targetMetadata.ToFile(filepath.Join(metadataDirectory, "1.targets.json"), false); err != nil {
		return err
	}
	if err := snapshot.ToFile(filepath.Join(metadataDirectory, "1.snapshot.json"), false); err != nil {
		return err
	}
	if err := timestamp.ToFile(filepath.Join(metadataDirectory, "timestamp.json"), false); err != nil {
		return err
	}
	if err := VerifyFilesystem(temporary, expiresFrom); err != nil {
		return err
	}
	return os.Rename(temporary, output)
}

func collectTargets(workspace string, config *Workspace) (map[string]targetSource, catalog, error) {
	result := map[string]targetSource{}
	catalogValue := catalog{Schema: "clangup.catalog/v1", Repository: catalogRepository{
		Namespace: config.Namespace, DisplayName: config.DisplayName, DefaultChannel: config.DefaultChannel,
	}, Channels: map[string]catalogChannel{}}
	channelPaths, err := filepath.Glob(filepath.Join(workspace, "channels", "*.toml"))
	if err != nil {
		return nil, catalogValue, err
	}
	if len(channelPaths) == 0 {
		return nil, catalogValue, fmt.Errorf("workspace has no channels")
	}
	for _, channelPath := range channelPaths {
		var channel Channel
		if err := readTOML(channelPath, &channel); err != nil {
			return nil, catalogValue, err
		}
		if channel.Schema != "clangup.authoring-channel/v1" || channel.Current == "" {
			return nil, catalogValue, fmt.Errorf("channel is not publishable: %s", channelPath)
		}
		releasePaths, err := filepath.Glob(filepath.Join(workspace, "releases", channel.Name, "*", "release.toml"))
		if err != nil {
			return nil, catalogValue, err
		}
		entry := catalogChannel{Description: channel.Description, Current: channel.Current}
		for _, releasePath := range releasePaths {
			var release ImportedRelease
			if err := readTOML(releasePath, &release); err != nil {
				return nil, catalogValue, err
			}
			if release.Schema != ReleaseSchema || release.Release.Channel != channel.Name {
				return nil, catalogValue, fmt.Errorf("invalid imported release: %s", releasePath)
			}
			exact := fmt.Sprintf("%s-%d", release.Release.Version, release.Release.Release)
			item := catalogRelease{Version: release.Release.Version, Release: release.Release.Release, ReleasedAt: release.ReleasedAt, Changelog: release.Changelog}
			result[fmt.Sprintf("build-specs/%s/%s.lock.json", channel.Name, exact)] = targetSource{Object: release.LockedSpecSHA256}
			for _, artifact := range release.Artifacts {
				manifestTarget := fmt.Sprintf("manifests/%s/%s/%s.json", channel.Name, exact, artifact.Target)
				result[manifestTarget] = targetSource{Object: artifact.ManifestSHA256}
				result[fmt.Sprintf("artifacts/%s.%s", artifact.PayloadSHA256, artifact.Name)] = targetSource{Object: artifact.PayloadSHA256}
				result[fmt.Sprintf("build-records/%s/%s/%s.json", channel.Name, exact, artifact.Target)] = targetSource{Object: artifact.BuildRecordSHA256}
				item.Artifacts = append(item.Artifacts, catalogArtifact{Target: artifact.Target, Manifest: manifestTarget})
			}
			for _, object := range release.Objects {
				extension := ".patch"
				directory := "patches"
				if object.Kind == "source" {
					extension = ".tar.xz"
					directory = "sources"
				}
				result[fmt.Sprintf("%s/sha256/%s%s", directory, object.SHA256, extension)] = targetSource{Object: object.SHA256}
			}
			sort.Slice(item.Artifacts, func(i, j int) bool { return item.Artifacts[i].Target < item.Artifacts[j].Target })
			entry.Releases = append(entry.Releases, item)
		}
		if !containsExact(entry.Releases, channel.Current) {
			return nil, catalogValue, fmt.Errorf("channel current release is missing: %s@%s", channel.Name, channel.Current)
		}
		sort.Slice(entry.Releases, func(i, j int) bool {
			if entry.Releases[i].Version != entry.Releases[j].Version {
				return entry.Releases[i].Version < entry.Releases[j].Version
			}
			return entry.Releases[i].Release < entry.Releases[j].Release
		})
		catalogValue.Channels[channel.Name] = entry
	}
	return result, catalogValue, nil
}

func containsExact(releases []catalogRelease, exact string) bool {
	for _, release := range releases {
		if fmt.Sprintf("%s-%d", release.Version, release.Release) == exact {
			return true
		}
	}
	return false
}

func metaFile(version int64, contents []byte) *metadata.MetaFiles {
	digest := sha256.Sum256(contents)
	result := metadata.MetaFile(version)
	result.Length = int64(len(contents))
	result.Hashes["sha256"] = metadata.HexBytes(digest[:])
	return result
}

func consistentTargetPath(root, logical, digest string) string {
	directory, name := filepath.Split(filepath.FromSlash(logical))
	return filepath.Join(root, directory, digest+"."+name)
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

func VerifyFilesystem(root string, now time.Time) error {
	metadataRoot := filepath.Join(root, "metadata")
	rootMeta, err := metadata.Root().FromFile(filepath.Join(metadataRoot, "root.json"))
	if err != nil {
		return err
	}
	targetsMeta, err := metadata.Targets().FromFile(filepath.Join(metadataRoot, "1.targets.json"))
	if err != nil {
		return err
	}
	snapshotMeta, err := metadata.Snapshot().FromFile(filepath.Join(metadataRoot, "1.snapshot.json"))
	if err != nil {
		return err
	}
	timestampMeta, err := metadata.Timestamp().FromFile(filepath.Join(metadataRoot, "timestamp.json"))
	if err != nil {
		return err
	}
	if rootMeta.Signed.IsExpired(now) || targetsMeta.Signed.IsExpired(now) || snapshotMeta.Signed.IsExpired(now) || timestampMeta.Signed.IsExpired(now) {
		return fmt.Errorf("repository metadata is expired")
	}
	if err := rootMeta.VerifyDelegate("root", rootMeta); err != nil {
		return err
	}
	if err := rootMeta.VerifyDelegate("targets", targetsMeta); err != nil {
		return err
	}
	if err := rootMeta.VerifyDelegate("snapshot", snapshotMeta); err != nil {
		return err
	}
	if err := rootMeta.VerifyDelegate("timestamp", timestampMeta); err != nil {
		return err
	}
	targetBytes, err := os.ReadFile(filepath.Join(metadataRoot, "1.targets.json"))
	if err != nil {
		return err
	}
	if err := snapshotMeta.Signed.Meta["targets.json"].VerifyLengthHashes(targetBytes); err != nil {
		return err
	}
	snapshotBytes, err := os.ReadFile(filepath.Join(metadataRoot, "1.snapshot.json"))
	if err != nil {
		return err
	}
	if err := timestampMeta.Signed.Meta["snapshot.json"].VerifyLengthHashes(snapshotBytes); err != nil {
		return err
	}
	for logical, info := range targetsMeta.Signed.Targets {
		digest := hex.EncodeToString(info.Hashes["sha256"])
		path := consistentTargetPath(filepath.Join(root, "targets"), logical, digest)
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := info.VerifyLengthHashes(contents); err != nil {
			return fmt.Errorf("verify target %s: %w", logical, err)
		}
	}
	return nil
}

func ParseExpiry(value string) (time.Time, error) {
	if value == "" {
		return time.Now().UTC().Truncate(time.Second), nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func RepositoryTargetPaths(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(filepath.Join(root, "targets"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			relative, _ := filepath.Rel(root, path)
			paths = append(paths, filepath.ToSlash(relative))
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}
