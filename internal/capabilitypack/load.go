package capabilitypack

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"contractaudit/internal/auditext"
)

// LoadDir reads and validates every regular "<name>.json" manifest in dir.
// Non-JSON regular files are ignored. Directory entries, symlinks and other
// non-regular files are rejected so a pack directory cannot escape the
// deployment-controlled path. A directory with no manifests is an error.
func LoadDir(dir string) ([]Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: read capability pack directory %q: %v", auditext.ErrInvalidRequest, dir, err)
	}

	manifests := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		manifest, include, err := loadEntry(dir, entry)
		if err != nil {
			return nil, err
		}
		if !include {
			continue
		}
		manifests = append(manifests, manifest)
	}
	if len(manifests) == 0 {
		return nil, fmt.Errorf("%w: no capability pack manifests in %q", auditext.ErrInvalidRequest, dir)
	}
	return manifests, nil
}

func loadEntry(dir string, entry os.DirEntry) (Manifest, bool, error) {
	name := entry.Name()
	if err := validateDirectoryEntry(entry); err != nil {
		return Manifest{}, false, err
	}
	if !strings.HasSuffix(name, ".json") {
		return Manifest{}, false, nil
	}
	raw, err := readManifestFile(filepath.Join(dir, name))
	if err != nil {
		return Manifest{}, false, err
	}
	manifest, err := Parse(raw)
	if err != nil {
		return Manifest{}, false, fmt.Errorf("%w: capability pack file %q: %v", auditext.ErrInvalidRequest, name, err)
	}
	return manifest, true, nil
}

func validateDirectoryEntry(entry os.DirEntry) error {
	name := entry.Name()
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: capability pack entry %q is a symlink", auditext.ErrInvalidRequest, name)
	}
	if entry.IsDir() {
		return fmt.Errorf("%w: capability pack entry %q is a directory", auditext.ErrInvalidRequest, name)
	}
	if !entry.Type().IsRegular() {
		return fmt.Errorf("%w: capability pack entry %q is not a regular file", auditext.ErrInvalidRequest, name)
	}
	return nil
}

func readManifestFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: open capability pack file %q: %v", auditext.ErrInvalidRequest, path, err)
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, MaxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read capability pack file %q: %v", auditext.ErrInvalidRequest, path, err)
	}
	if len(raw) > MaxManifestBytes {
		return nil, fmt.Errorf("%w: capability pack file %q exceeds %d bytes", auditext.ErrInvalidRequest, path, MaxManifestBytes)
	}
	return raw, nil
}
