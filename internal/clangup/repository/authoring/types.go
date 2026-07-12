package authoring

const (
	WorkspaceSchema = "clangup.repository-authoring/v1"
	ReleaseSchema   = "clangup.authoring-release/v1"
)

type Workspace struct {
	Schema         string `toml:"schema"`
	Namespace      string `toml:"namespace"`
	DisplayName    string `toml:"display_name"`
	DefaultChannel string `toml:"default_channel,omitempty"`
}

type Channel struct {
	Schema      string `json:"schema" toml:"schema"`
	Name        string `json:"name" toml:"name"`
	Description string `json:"description,omitempty" toml:"description,omitempty"`
	Current     string `json:"current,omitempty" toml:"current,omitempty"`
}

type ReleaseIdentity struct {
	Channel string `json:"channel" toml:"channel"`
	Version string `json:"version" toml:"version"`
	Release int    `json:"release" toml:"release"`
}

type ImportedArtifact struct {
	Target            string `json:"target" toml:"target"`
	Name              string `json:"name" toml:"name"`
	PayloadSHA256     string `json:"payload_sha256" toml:"payload_sha256"`
	ManifestSHA256    string `json:"manifest_sha256" toml:"manifest_sha256"`
	BuildRecordSHA256 string `json:"build_record_sha256" toml:"build_record_sha256"`
}

type ImportedObject struct {
	Kind   string `json:"kind" toml:"kind"`
	SHA256 string `json:"sha256" toml:"sha256"`
	Name   string `json:"name,omitempty" toml:"name,omitempty"`
}

type ImportedRelease struct {
	Schema           string             `json:"schema" toml:"schema"`
	Release          ReleaseIdentity    `json:"release" toml:"release"`
	LockedSpecSHA256 string             `json:"locked_spec_sha256" toml:"locked_spec_sha256"`
	Artifacts        []ImportedArtifact `json:"artifacts" toml:"artifacts"`
	Objects          []ImportedObject   `json:"objects" toml:"objects"`
	Changelog        string             `json:"changelog,omitempty" toml:"changelog,omitempty"`
	ReleasedAt       string             `json:"released_at" toml:"released_at"`
}

type Bundle struct {
	Schema           string           `json:"schema"`
	Channel          string           `json:"channel"`
	Version          string           `json:"version"`
	Release          int              `json:"release"`
	LockedSpec       string           `json:"locked_spec"`
	LockedSpecSHA256 string           `json:"locked_spec_sha256"`
	Artifacts        []BundleArtifact `json:"artifacts"`
	Objects          []BundleObject   `json:"objects"`
}

type BundleArtifact struct {
	Target            string `json:"target"`
	Manifest          string `json:"manifest"`
	ManifestSHA256    string `json:"manifest_sha256"`
	Payload           string `json:"payload"`
	PayloadSHA256     string `json:"payload_sha256"`
	BuildRecord       string `json:"build_record"`
	BuildRecordSHA256 string `json:"build_record_sha256"`
}

type BuildRecord struct {
	Schema           string          `json:"schema"`
	Release          ReleaseIdentity `json:"release"`
	Target           string          `json:"target"`
	LockedSpecSHA256 string          `json:"locked_spec_sha256"`
	ArtifactSHA256   string          `json:"artifact_sha256"`
}

type BundleObject struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type ArtifactManifest struct {
	Schema   string          `json:"schema"`
	Release  ReleaseIdentity `json:"release"`
	Artifact struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"artifact"`
	RuntimeRequirements struct {
		Triple string `json:"triple"`
	} `json:"runtime_requirements"`
	Source SourceContract `json:"source"`
}

type SourceContract struct {
	Archive struct {
		SHA256 string `json:"sha256"`
		Target string `json:"target"`
	} `json:"archive"`
	Patches []struct {
		SHA256 string `json:"sha256"`
		Target string `json:"target"`
	} `json:"patches"`
	PatchsetSHA256 string `json:"patchset_sha256"`
}

type LockedSource struct {
	SHA256  string `json:"sha256"`
	Patches []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"patches"`
	PatchsetSHA256 string `json:"patchset_sha256"`
}

type LockedSpec struct {
	Schema  string          `json:"schema"`
	Release ReleaseIdentity `json:"release"`
	Source  LockedSource    `json:"source"`
	Targets []struct {
		Triple   string `json:"triple"`
		Required bool   `json:"required"`
	} `json:"targets"`
	Changelog []struct {
		Release int    `json:"release"`
		Date    string `json:"date"`
		Summary string `json:"summary"`
	} `json:"changelog"`
}
