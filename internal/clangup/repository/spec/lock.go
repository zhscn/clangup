package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

func Lock(loaded *Loaded) (*LockFile, error) {
	if loaded == nil {
		return nil, fmt.Errorf("loaded spec is nil")
	}
	patchsetDigest, err := patchsetSHA256(loaded.Patches)
	if err != nil {
		return nil, err
	}
	locked := &LockFile{
		Schema: "clangup.build-lock/v1",
		Release: ReleaseIdentity{
			Channel: loaded.Spec.Channel,
			Version: loaded.Spec.Version,
			Release: loaded.Spec.Release,
		},
		Source: LockedSource{
			URL:            loaded.Spec.Source.URL,
			SHA256:         loaded.Spec.Source.SHA256,
			Patches:        append([]LockedPatch{}, loaded.Patches...),
			PatchsetSHA256: patchsetDigest,
		},
		Changelog: slices.Clone(loaded.Spec.Changelog),
	}

	locked.Targets = make([]LockedTarget, 0, len(loaded.Spec.Targets))
	for _, target := range loaded.Spec.Targets {
		distribution := cloneDistribution(loaded.Spec.Distribution)
		if target.Distribution != nil {
			distribution = cloneDistribution(*target.Distribution)
		}
		driver := loaded.Spec.Driver
		if target.Driver != nil {
			driver = *target.Driver
		}
		delivery := make(map[string]RuntimeDelivery)
		for name, policy := range loaded.Spec.RuntimeDelivery {
			if slices.Contains(distribution.Runtimes, name) {
				delivery[name] = policy
			}
		}
		lockedTarget := LockedTarget{
			OS:                 target.OS,
			Arch:               target.Arch,
			Triple:             target.Triple,
			MinMacOSVersion:    target.MinMacOSVersion,
			CPUISA:             target.CPUISA,
			DriverRequirements: sortedClone(target.DriverRequirements),
			Required:           target.Required,
			Distribution:       normalizedDistribution(distribution),
			RuntimeDelivery:    delivery,
			Driver:             driver,
		}
		if target.OS == "linux" {
			lockedTarget.Libc = &LibcRequirement{Name: target.Libc, MinVersion: target.LibcVersion}
		}
		locked.Targets = append(locked.Targets, lockedTarget)
	}
	sort.Slice(locked.Targets, func(left, right int) bool {
		return locked.Targets[left].Triple < locked.Targets[right].Triple
	})
	sort.Slice(locked.Changelog, func(left, right int) bool {
		return locked.Changelog[left].Release < locked.Changelog[right].Release
	})
	return locked, nil
}

func MarshalCanonical(locked *LockFile) ([]byte, error) {
	// Lock v1 contains only structs, arrays, strings, integers, booleans, and
	// string-keyed maps. encoding/json sorts map keys and produces a compact,
	// deterministic representation for that restricted data model.
	contents, err := json.Marshal(locked)
	if err != nil {
		return nil, err
	}
	return contents, nil
}

func patchsetSHA256(patches []LockedPatch) (string, error) {
	identity := make([]struct {
		SHA256 string `json:"sha256"`
		Strip  int    `json:"strip"`
	}, 0, len(patches))
	for _, patch := range patches {
		identity = append(identity, struct {
			SHA256 string `json:"sha256"`
			Strip  int    `json:"strip"`
		}{SHA256: patch.SHA256, Strip: patch.Strip})
	}
	contents, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("encode patchset identity: %w", err)
	}
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:]), nil
}

func cloneDistribution(distribution Distribution) Distribution {
	return Distribution{
		Projects: slices.Clone(distribution.Projects),
		Runtimes: slices.Clone(distribution.Runtimes),
	}
}

func normalizedDistribution(distribution Distribution) Distribution {
	distribution.Projects = sortedClone(distribution.Projects)
	distribution.Runtimes = sortedClone(distribution.Runtimes)
	return distribution
}

func sortedClone(values []string) []string {
	result := slices.Clone(values)
	sort.Strings(result)
	return result
}
