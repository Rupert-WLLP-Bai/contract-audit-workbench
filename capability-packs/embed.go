// Package capabilitypacks embeds the declarative manifests shipped with the
// host. The files are data only: no scripts, SQL or executable code are loaded.
package capabilitypacks

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed *.json
var manifests embed.FS

// Manifests returns every embedded manifest in stable filename order.
func Manifests() ([][]byte, error) {
	entries, err := fs.ReadDir(manifests, ".")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	out := make([][]byte, 0, len(names))
	for _, name := range names {
		raw, err := manifests.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no embedded capability pack manifests")
	}
	return out, nil
}
