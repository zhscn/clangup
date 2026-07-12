package authoring

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/zhscn/clangup/internal/clangup/toolchain"
)

type ObjectInfo struct {
	Size   int64
	SHA256 string
	ETag   string
}

type PutOptions struct {
	ContentType  string
	CacheControl string
	SHA256       string
	IfMatch      string
	IfNoneMatch  bool
}

type Store interface {
	Head(key string) (*ObjectInfo, error)
	Get(key string) ([]byte, error)
	Put(key string, contents []byte, options PutOptions) error
}

type PublishOptions struct {
	Namespace      string
	DisplayName    string
	DefaultChannel string
	CatalogKey     string
	DryRun         bool
}

type PublishResult struct {
	Schema        string `json:"schema"`
	Namespace     string `json:"namespace"`
	Channel       string `json:"channel"`
	Exact         string `json:"exact"`
	DescriptorKey string `json:"descriptor_key"`
	CatalogKey    string `json:"catalog_key"`
	CatalogSHA256 string `json:"catalog_sha256"`
	Written       bool   `json:"written"`
}

func Publish(store Store, releasePath string, options PublishOptions) (*PublishResult, error) {
	if options.Namespace == "" || options.DisplayName == "" || options.CatalogKey == "" {
		return nil, fmt.Errorf("namespace, display name, and catalog key are required")
	}
	contents, err := os.ReadFile(releasePath)
	if err != nil {
		return nil, err
	}
	var release toolchain.Release
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release descriptor: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("decode release descriptor: trailing JSON data")
	}
	if release.Schema != "clangup.release/v1" || release.Release.Channel == "" || release.Release.Version == "" || release.Release.Release < 1 {
		return nil, fmt.Errorf("invalid release descriptor identity")
	}
	if err := validateReleaseObjects(&release); err != nil {
		return nil, err
	}
	exact := fmt.Sprintf("%s-%d", release.Release.Version, release.Release.Release)
	descriptorKey := fmt.Sprintf("releases/%s/%s/release.json", release.Release.Channel, exact)
	descriptorDigest := sha256.Sum256(contents)
	descriptor := toolchain.Object{Key: descriptorKey, Size: int64(len(contents)), SHA256: hex.EncodeToString(descriptorDigest[:])}
	remote, err := store.Head(descriptorKey)
	if err != nil {
		return nil, fmt.Errorf("inspect release descriptor: %w", err)
	}
	if remote == nil {
		return nil, fmt.Errorf("release descriptor is not uploaded: %s", descriptorKey)
	}
	if remote.Size != descriptor.Size || remote.SHA256 != descriptor.SHA256 {
		return nil, fmt.Errorf("remote release descriptor identity differs: %s", descriptorKey)
	}

	catalog := toolchain.Catalog{Schema: "clangup.catalog/v1", Channels: map[string]toolchain.CatalogChannel{}}
	catalog.Repository.Namespace = options.Namespace
	catalog.Repository.DisplayName = options.DisplayName
	var etag string
	existing, err := store.Head(options.CatalogKey)
	if err != nil {
		return nil, fmt.Errorf("inspect catalog: %w", err)
	}
	if existing != nil {
		catalogContents, err := store.Get(options.CatalogKey)
		if err != nil {
			return nil, fmt.Errorf("download catalog: %w", err)
		}
		if err := json.Unmarshal(catalogContents, &catalog); err != nil {
			return nil, fmt.Errorf("decode catalog: %w", err)
		}
		if catalog.Schema != "clangup.catalog/v1" || catalog.Repository.Namespace != options.Namespace {
			return nil, fmt.Errorf("existing catalog identity differs")
		}
		catalog.Repository.DisplayName = options.DisplayName
		etag = existing.ETag
	}
	if options.DefaultChannel != "" {
		catalog.Repository.DefaultChannel = options.DefaultChannel
	}
	entry := catalog.Channels[release.Release.Channel]
	entry.Current = exact
	filtered := entry.Releases[:0]
	for _, item := range entry.Releases {
		if item.Version != release.Release.Version || item.Release != release.Release.Release {
			filtered = append(filtered, item)
		}
	}
	entry.Releases = append(filtered, toolchain.CatalogRelease{Version: release.Release.Version, Release: release.Release.Release, Descriptor: descriptor})
	sort.Slice(entry.Releases, func(i, j int) bool {
		if entry.Releases[i].Version != entry.Releases[j].Version {
			return entry.Releases[i].Version < entry.Releases[j].Version
		}
		return entry.Releases[i].Release < entry.Releases[j].Release
	})
	catalog.Channels[release.Release.Channel] = entry
	catalogContents, err := json.Marshal(catalog)
	if err != nil {
		return nil, err
	}
	catalogContents = append(catalogContents, '\n')
	catalogDigest := sha256.Sum256(catalogContents)
	result := &PublishResult{
		Schema: "clangup.repo-publish/v1", Namespace: options.Namespace,
		Channel: release.Release.Channel, Exact: exact, DescriptorKey: descriptorKey,
		CatalogKey: options.CatalogKey, CatalogSHA256: hex.EncodeToString(catalogDigest[:]),
	}
	if options.DryRun {
		return result, nil
	}
	if existing != nil && existing.SHA256 == result.CatalogSHA256 {
		return result, nil
	}
	put := PutOptions{ContentType: "application/json", CacheControl: "no-cache", SHA256: result.CatalogSHA256}
	if etag == "" {
		put.IfNoneMatch = true
	} else {
		put.IfMatch = etag
	}
	if err := store.Put(options.CatalogKey, catalogContents, put); err != nil {
		return nil, fmt.Errorf("conditionally update catalog: %w", err)
	}
	result.Written = true
	return result, nil
}

func validateReleaseObjects(release *toolchain.Release) error {
	objects := []toolchain.Object{release.LockedSpec, release.Source}
	objects = append(objects, release.Patches...)
	seenTargets := make(map[string]struct{}, len(release.Artifacts))
	for _, artifact := range release.Artifacts {
		if artifact.Target == "" {
			return fmt.Errorf("release artifact has an empty target")
		}
		if _, exists := seenTargets[artifact.Target]; exists {
			return fmt.Errorf("release artifact target is duplicated: %s", artifact.Target)
		}
		seenTargets[artifact.Target] = struct{}{}
		objects = append(objects, artifact.Artifact, artifact.Manifest, artifact.BuildRecord)
	}
	for _, object := range objects {
		if len(object.SHA256) != 64 || object.Size < 0 || !strings.HasPrefix(object.Key, "objects/sha256/"+object.SHA256+"/") {
			return fmt.Errorf("invalid content-addressed object: %s", object.Key)
		}
		if _, err := hex.DecodeString(object.SHA256); err != nil {
			return fmt.Errorf("invalid object SHA-256: %s", object.SHA256)
		}
	}
	return nil
}
