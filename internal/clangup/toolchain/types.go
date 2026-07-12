package toolchain

type Object struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type ReleaseIdentity struct {
	Channel string `json:"channel"`
	Version string `json:"version"`
	Release int    `json:"release"`
}

type Catalog struct {
	Schema     string `json:"schema"`
	Repository struct {
		Namespace      string `json:"namespace"`
		DisplayName    string `json:"display_name"`
		DefaultChannel string `json:"default_channel,omitempty"`
	} `json:"repository"`
	Channels map[string]CatalogChannel `json:"channels"`
}

type CatalogChannel struct {
	Current  string           `json:"current"`
	Releases []CatalogRelease `json:"releases"`
}

type CatalogRelease struct {
	Version    string `json:"version"`
	Release    int    `json:"release"`
	Descriptor Object `json:"descriptor"`
}

type Release struct {
	Schema    string          `json:"schema"`
	Release   ReleaseIdentity `json:"release"`
	Inputs    ReleaseInputs   `json:"inputs"`
	Artifacts []Artifact      `json:"artifacts"`
}

type ReleaseInputs struct {
	LockedSpec     Object   `json:"locked_spec"`
	Source         Object   `json:"source"`
	Patches        []Object `json:"patches"`
	PatchsetSHA256 string   `json:"patchset_sha256"`
}

type Artifact struct {
	Target   string         `json:"target"`
	Artifact Object         `json:"artifact"`
	Manifest Object         `json:"manifest"`
	Build    map[string]any `json:"build"`
}

type Manifest struct {
	Schema   string          `json:"schema"`
	Release  ReleaseIdentity `json:"release"`
	Artifact struct {
		Name        string `json:"name"`
		Size        int64  `json:"size"`
		SHA256      string `json:"sha256"`
		Compression string `json:"compression"`
		PayloadRoot string `json:"payload_root"`
		Relocatable bool   `json:"relocatable"`
	} `json:"artifact"`
	RuntimeRequirements struct {
		OS              string `json:"os"`
		Arch            string `json:"arch"`
		Triple          string `json:"triple"`
		MinMacOSVersion string `json:"min_macos_version,omitempty"`
		Libc            *struct {
			Name       string `json:"name"`
			MinVersion string `json:"min_version"`
		} `json:"libc,omitempty"`
		CPUISA string `json:"cpu_isa,omitempty"`
	} `json:"runtime_requirements"`
	DriverRequirements struct {
		ExternalComponents []string `json:"external_components"`
	} `json:"driver_requirements"`
	Driver map[string]any `json:"driver"`
	Source struct {
		Archive struct {
			SHA256 string `json:"sha256"`
		} `json:"archive"`
		PatchsetSHA256 string `json:"patchset_sha256"`
	} `json:"source"`
}

type RepositoryConfig struct {
	Schema       string       `json:"schema"`
	Repositories []Repository `json:"repositories"`
}

type Repository struct {
	Namespace string `json:"namespace"`
	URL       string `json:"url"`
}
