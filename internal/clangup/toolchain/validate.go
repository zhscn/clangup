package toolchain

import (
	"fmt"
	pathpkg "path"
)

func ValidateManifest(release *Release, artifact *Artifact, manifest *Manifest) error {
	if manifest.Schema != "clangup.artifact/v1" || manifest.Release != release.Release {
		return fmt.Errorf("manifest release identity mismatch for %s", artifact.Target)
	}
	if manifest.RuntimeRequirements.Triple != artifact.Target {
		return fmt.Errorf("manifest target mismatch for %s", artifact.Target)
	}
	if manifest.Artifact.Name != pathpkg.Base(artifact.Artifact.Key) || manifest.Artifact.Size != artifact.Artifact.Size || manifest.Artifact.SHA256 != artifact.Artifact.SHA256 {
		return fmt.Errorf("manifest artifact identity mismatch for %s", artifact.Target)
	}
	if manifest.Artifact.Compression != "tar.zst" || manifest.Artifact.PayloadRoot != "prefix" || !manifest.Artifact.Relocatable {
		return fmt.Errorf("unsupported artifact payload contract for %s", artifact.Target)
	}
	if manifest.Source.Archive.SHA256 != release.Source.SHA256 {
		return fmt.Errorf("manifest source identity mismatch for %s", artifact.Target)
	}
	return nil
}
