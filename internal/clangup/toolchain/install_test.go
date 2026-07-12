package toolchain

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestInstallArchive(t *testing.T) {
	archive := makeArchive(t, []archiveEntry{
		{name: "bin/clang", contents: "clang", mode: 0o755},
		{name: "bin/clang++", contents: "clang++", mode: 0o755},
		{name: "bin/cc", link: "clang"},
	})
	destination := filepath.Join(t.TempDir(), "toolchain")
	if err := InstallArchive(archive, destination); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(destination, "bin", "cc")); err != nil || target != "clang" {
		t.Fatalf("symlink = %q, %v", target, err)
	}
}

func TestInstallRejectsEscapingSymlink(t *testing.T) {
	archive := makeArchive(t, []archiveEntry{
		{name: "bin/clang", contents: "clang", mode: 0o755},
		{name: "bin/clang++", contents: "clang++", mode: 0o755},
		{name: "bin/escape", link: "../../outside"},
	})
	if err := InstallArchive(archive, filepath.Join(t.TempDir(), "toolchain")); err == nil {
		t.Fatal("escaping symlink was accepted")
	}
}

type archiveEntry struct {
	name, contents, link string
	mode                 int64
}

func makeArchive(t *testing.T, entries []archiveEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.tar.zst")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(file)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(encoder)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.contents)), Typeflag: tar.TypeReg}
		if entry.link != "" {
			header.Typeflag = tar.TypeSymlink
			header.Linkname = entry.link
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.contents != "" {
			if _, err := writer.Write([]byte(entry.contents)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
