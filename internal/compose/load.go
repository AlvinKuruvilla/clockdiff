// Package compose reads a compose project and reports what can be decided
// from the file alone.
package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
)

// LoadProject parses one compose file the way Compose itself would: ${VAR}
// interpolation, merge keys, extends, includes.
func LoadProject(path string) (*types.Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", path, err)
	}

	details := types.ConfigDetails{
		WorkingDir:  filepath.Dir(abs),
		ConfigFiles: []types.ConfigFile{{Filename: abs}},
		Environment: processEnvironment(),
	}

	return loader.LoadWithContext(context.Background(), details, func(o *loader.Options) {
		// Validation only requires a non-empty name; nothing here reads it.
		o.SetProjectName("clockdiff", true)

		// Resolving `environment` reads every env_file off disk, so a missing
		// .env fails the whole load. Nothing here reads `environment`.
		o.SkipResolveEnvironment = true
	})
}

func processEnvironment() types.Mapping {
	env := make(types.Mapping)
	for _, kv := range os.Environ() {
		if key, value, ok := strings.Cut(kv, "="); ok {
			env[key] = value
		}
	}
	return env
}
