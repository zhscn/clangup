package toolchain

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

func InstallArchive(archive, destination string) error {
	destinationExists := false
	if entries, err := os.ReadDir(destination); err == nil && len(entries) != 0 {
		return fmt.Errorf("installation prefix is not empty: %s", destination)
	} else if err == nil {
		destinationExists = true
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(parent, ".clangup-install-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := extractArchive(archive, temporary); err != nil {
		return err
	}
	for _, required := range []string{"bin/clang", "bin/clang++"} {
		info, err := os.Stat(filepath.Join(temporary, required))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("artifact is missing %s", required)
		}
	}
	if destinationExists {
		if err := os.Remove(destination); err != nil {
			return err
		}
	}
	return os.Rename(temporary, destination)
}

func extractArchive(archive, root string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		return err
	}
	defer decoder.Close()
	reader := tar.NewReader(decoder)
	var entries, totalSize int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		entries++
		totalSize += header.Size
		if entries > 1_000_000 || totalSize > 20<<30 {
			return fmt.Errorf("artifact exceeds extraction limits")
		}
		relative, err := safeArchivePath(header.Name)
		if err != nil {
			return err
		}
		destination := filepath.Join(root, relative)
		if err := ensureParent(root, filepath.Dir(destination)); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			mode := os.FileMode(0o644)
			if header.Mode&0o111 != 0 {
				mode = 0o755
			}
			output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, reader)
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if err := safeSymlink(relative, header.Linkname); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, destination); err != nil {
				return err
			}
		case tar.TypeLink:
			target, err := safeArchivePath(header.Linkname)
			if err != nil {
				return err
			}
			targetPath := filepath.Join(root, target)
			info, err := os.Lstat(targetPath)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("hardlink target is not an existing regular file: %s", header.Linkname)
			}
			if err := os.Link(targetPath, destination); err != nil {
				return err
			}
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			continue
		default:
			return fmt.Errorf("unsupported archive entry type %d for %s", header.Typeflag, header.Name)
		}
	}
}

func safeArchivePath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) || filepath.IsAbs(name) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return clean, nil
}

func safeSymlink(entry, link string) error {
	if link == "" || filepath.IsAbs(link) {
		return fmt.Errorf("unsafe symlink %s -> %s", entry, link)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(entry), filepath.FromSlash(link)))
	if resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return fmt.Errorf("symlink escapes installation prefix: %s -> %s", entry, link)
	}
	return nil
}

func ensureParent(root, directory string) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive path escapes installation prefix")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if info, err := os.Lstat(current); err == nil {
			if !info.IsDir() {
				return fmt.Errorf("archive parent is not a directory: %s", current)
			}
		} else if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	return nil
}
