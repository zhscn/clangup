package cmk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CMake file API reply plumbing, shared by target discovery (codemodel)
// and staleness detection (cmakeFiles). Replies are read through the
// newest index-*.json per the file API protocol: reply files from a
// superseded generation may briefly coexist, and only the index says
// which ones are current.

type replyIndexEntry struct {
	JSONFile string `json:"jsonFile"`
	Error    string `json:"error"`
}

type replyIndex struct {
	Reply map[string]replyIndexEntry `json:"reply"`
}

// readReplyObject unmarshals the reply for one shared stateless query
// (e.g. "codemodel-v2") into out. Index file names embed the generation
// timestamp, so the lexicographically largest one is the newest.
func readReplyObject(replyDir, query string, out any) error {
	entries, err := os.ReadDir(replyDir)
	if err != nil {
		return err
	}
	var indexName string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "index-") && strings.HasSuffix(name, ".json") && name > indexName {
			indexName = name
		}
	}
	if indexName == "" {
		return fmt.Errorf("no file API index in %s", replyDir)
	}
	data, err := os.ReadFile(filepath.Join(replyDir, indexName))
	if err != nil {
		return err
	}
	var idx replyIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return fmt.Errorf("%s: %w", indexName, err)
	}
	ref, ok := idx.Reply[query]
	if !ok {
		return fmt.Errorf("no %s reply in %s", query, replyDir)
	}
	if ref.Error != "" {
		return fmt.Errorf("%s reply: %s", query, ref.Error)
	}
	data, err = os.ReadFile(filepath.Join(replyDir, ref.JSONFile))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// cmakeFilesReply is the slice of the cmakeFiles object cmk needs: every
// file CMake read while configuring (the exact set whose changes require
// a reconfigure) and the recorded CONFIGURE_DEPENDS glob results.
type cmakeFilesReply struct {
	Paths struct {
		Source string `json:"source"`
		Build  string `json:"build"`
	} `json:"paths"`
	Inputs []struct {
		// Path is relative to the source dir when inside it, absolute
		// otherwise, always with forward slashes.
		Path        string `json:"path"`
		IsGenerated bool   `json:"isGenerated"`
		IsExternal  bool   `json:"isExternal"`
		IsCMake     bool   `json:"isCMake"`
	} `json:"inputs"`
	GlobsDependent []globDependent `json:"globsDependent"`
}

// globDependent is one file(GLOB ... CONFIGURE_DEPENDS) call: the
// expression and the paths it matched at configure time.
type globDependent struct {
	Expression      string   `json:"expression"`
	Recurse         bool     `json:"recurse"`
	ListDirectories bool     `json:"listDirectories"`
	FollowSymlinks  bool     `json:"followSymlinks"`
	Relative        string   `json:"relative"`
	Paths           []string `json:"paths"`
}

func readCMakeFilesReply(buildDir string) (*cmakeFilesReply, error) {
	var cf cmakeFilesReply
	if err := readReplyObject(filepath.Join(buildDir, ".cmake/api/v1/reply"), "cmakeFiles-v1", &cf); err != nil {
		return nil, err
	}
	return &cf, nil
}
