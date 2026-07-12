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

type Index struct {
	Schema         string                  `json:"schema"`
	DefaultChannel string                  `json:"default_channel"`
	Channels       map[string]IndexChannel `json:"channels"`
}

type IndexChannel struct {
	Current  string         `json:"current"`
	Releases []IndexRelease `json:"releases"`
}

type IndexRelease struct {
	Version string `json:"version"`
	Release int    `json:"release"`
	Path    string `json:"path"`
}

type Release struct {
	Schema    string          `json:"schema"`
	Release   ReleaseIdentity `json:"release"`
	Artifacts []Artifact      `json:"artifacts"`
}

type Artifact struct {
	Target   string `json:"target"`
	Artifact Object `json:"artifact"`
	Manifest Object `json:"manifest"`
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
	Driver       map[string]any `json:"driver"`
	Optimization map[string]any `json:"optimization,omitempty"`
	Build        map[string]any `json:"build,omitempty"`
	Source       struct {
		Archive struct {
			SHA256 string `json:"sha256"`
		} `json:"archive"`
		PatchsetSHA256 string `json:"patchset_sha256"`
	} `json:"source"`
}
