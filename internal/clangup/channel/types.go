package channel

type Spec struct {
	Schema          string                     `yaml:"schema"`
	Channel         string                     `yaml:"channel"`
	Version         string                     `yaml:"version"`
	Release         int                        `yaml:"release"`
	Source          Source                     `yaml:"source"`
	Distribution    Distribution               `yaml:"distribution"`
	RuntimeDelivery map[string]RuntimeDelivery `yaml:"runtime_delivery"`
	Driver          Driver                     `yaml:"driver"`
	Targets         []Target                   `yaml:"targets"`
	Changelog       []ChangelogEntry           `yaml:"changelog"`
}

type Source struct {
	URL         string `yaml:"url"`
	SHA256      string `yaml:"sha256"`
	PatchSeries string `yaml:"patch_series"`
}

type Distribution struct {
	Projects []string `yaml:"projects" json:"projects"`
	Runtimes []string `yaml:"runtimes" json:"runtimes"`
}

type RuntimeDelivery struct {
	Linkage string `yaml:"linkage" json:"linkage"`
}

type Driver struct {
	Libc             string `yaml:"libc" json:"libc"`
	CXXStdlib        string `yaml:"cxx_stdlib" json:"cxx_stdlib"`
	CXXStdlibLinkage string `yaml:"cxx_stdlib_linkage" json:"cxx_stdlib_linkage"`
	Linker           string `yaml:"linker" json:"linker"`
	RTLib            string `yaml:"rtlib" json:"rtlib"`
	UnwindLib        string `yaml:"unwindlib" json:"unwindlib"`
}

type Target struct {
	OS                 string        `yaml:"os"`
	Arch               string        `yaml:"arch"`
	Triple             string        `yaml:"triple"`
	Libc               string        `yaml:"libc"`
	LibcVersion        string        `yaml:"libc_version"`
	MinMacOSVersion    string        `yaml:"min_macos_version"`
	CPUISA             string        `yaml:"cpu_isa"`
	DriverRequirements []string      `yaml:"driver_requirements"`
	Required           bool          `yaml:"required"`
	Distribution       *Distribution `yaml:"distribution"`
	Driver             *Driver       `yaml:"driver"`
	Optimization       Optimization  `yaml:"optimization"`
}

type Optimization struct {
	PGO  bool `yaml:"pgo" json:"pgo"`
	BOLT bool `yaml:"bolt" json:"bolt"`
}

type ChangelogEntry struct {
	Release int    `yaml:"release" json:"release"`
	Date    string `yaml:"date" json:"date"`
	Summary string `yaml:"summary" json:"summary"`
}

type PatchSeries struct {
	Schema  string       `yaml:"schema"`
	Strip   int          `yaml:"strip"`
	Patches []PatchEntry `yaml:"patches"`
}

type PatchEntry struct {
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
}

type Loaded struct {
	Path       string
	BundleRoot string
	Spec       Spec
	Patches    []LockedPatch
}

type LockFile struct {
	Schema    string           `json:"schema"`
	Release   ReleaseIdentity  `json:"release"`
	Source    LockedSource     `json:"source"`
	Targets   []LockedTarget   `json:"targets"`
	Changelog []ChangelogEntry `json:"changelog"`
}

type ReleaseIdentity struct {
	Channel string `json:"channel"`
	Version string `json:"version"`
	Release int    `json:"release"`
}

type LockedSource struct {
	URL            string        `json:"url"`
	SHA256         string        `json:"sha256"`
	Patches        []LockedPatch `json:"patches"`
	PatchsetSHA256 string        `json:"patchset_sha256"`
}

type LockedPatch struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Strip  int    `json:"strip"`
}

type LockedTarget struct {
	OS                 string                     `json:"os"`
	Arch               string                     `json:"arch"`
	Triple             string                     `json:"triple"`
	Libc               *LibcRequirement           `json:"libc,omitempty"`
	MinMacOSVersion    string                     `json:"min_macos_version,omitempty"`
	CPUISA             string                     `json:"cpu_isa,omitempty"`
	DriverRequirements []string                   `json:"driver_requirements"`
	Required           bool                       `json:"required"`
	Distribution       Distribution               `json:"distribution"`
	RuntimeDelivery    map[string]RuntimeDelivery `json:"runtime_delivery"`
	Driver             Driver                     `json:"driver"`
	Optimization       Optimization               `json:"optimization"`
}

type LibcRequirement struct {
	Name       string `json:"name"`
	MinVersion string `json:"min_version"`
}
