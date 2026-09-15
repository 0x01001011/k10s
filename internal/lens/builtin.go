package lens

import (
	"embed"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/0x01001011/k10s/internal/config"
)

//go:embed builtin/*.yaml
var builtinFS embed.FS

// Dir is the user's lens directory. It sits beside config.yaml unless
// overridden, matching how internal/plugin finds plugins/.
func Dir() string {
	if path := os.Getenv("K10S_LENS_DIR"); path != "" {
		return path
	}
	if path := config.Path(); path != "" {
		return filepath.Join(filepath.Dir(path), "lenses")
	}
	return ""
}

// Builtins parses the packs embedded in the binary. An error here is a bug
// in this repo rather than anything a user did, so errors are returned for
// the test that guards them and ignored at runtime.
func Builtins() (packs []Pack, errs []error) {
	entries, err := builtinFS.ReadDir("builtin")
	if err != nil {
		return nil, []error{err}
	}
	for _, e := range entries {
		if e.IsDir() || !isYAML(e.Name()) {
			continue
		}
		name := "builtin/" + e.Name()
		data, err := builtinFS.ReadFile(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		p, err := Parse(data, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		packs = append(packs, p)
	}
	sort.Slice(packs, func(i, j int) bool { return packs[i].Name < packs[j].Name })
	return packs, errs
}

var (
	builtinOnce  sync.Once
	builtinCache []Pack
)

func builtins() []Pack {
	builtinOnce.Do(func() {
		builtinCache, _ = Builtins()
	})
	return builtinCache
}
