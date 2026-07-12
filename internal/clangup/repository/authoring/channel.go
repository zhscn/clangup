package authoring

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var exactReleasePattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)*)-([1-9][0-9]*)$`)

func SetCurrent(workspace, channel, exact string) error {
	if _, err := LoadWorkspace(workspace); err != nil {
		return err
	}
	if !channelPattern.MatchString(channel) {
		return fmt.Errorf("invalid channel name %q", channel)
	}
	match := exactReleasePattern.FindStringSubmatch(exact)
	if match == nil {
		return fmt.Errorf("invalid exact release %q", exact)
	}
	releasePath := filepath.Join(workspace, "releases", channel, exact, "release.toml")
	if _, err := os.Stat(releasePath); err != nil {
		return fmt.Errorf("release is not imported: %s", exact)
	}
	path := filepath.Join(workspace, "channels", channel+".toml")
	channelData := Channel{Schema: "clangup.authoring-channel/v1", Name: channel, Current: exact}
	if _, err := os.ReadFile(path); err == nil {
		var current Channel
		if err := readTOML(path, &current); err != nil {
			return err
		}
		channelData.Description = current.Description
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeTOML(path, channelData)
}
