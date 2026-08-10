// Package compose reads a compose project and reports what can be decided
// from the file alone.
package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
)

// Discover returns the compose files Compose itself would load from dir when
// given no -f: the first recognised name, plus its override if one sits beside
// it. The search walks upward to the filesystem root, so a call from an
// unrelated directory can find a project several levels up — which is what
// Compose does.
func Discover(dir string) ([]string, error) {
	opts, err := cli.NewProjectOptions(nil,
		cli.WithWorkingDirectory(dir),
		cli.WithDefaultConfigPath,
	)
	if err != nil {
		return nil, fmt.Errorf("find a compose file: %w", err)
	}
	if len(opts.ConfigPaths) == 0 {
		return nil, fmt.Errorf("no compose file found in %s or any parent", dir)
	}
	return opts.ConfigPaths, nil
}

// LoadProject parses compose files the way Compose itself would: ${VAR}
// interpolation, merge keys, extends, includes, and later files overriding
// earlier ones.
//
// Passing more than one matters even when the caller only knows of one.
// Compose merges docker-compose.override.yml automatically, and reading the
// base file alone describes a stack nobody runs.
func LoadProject(paths ...string) (*types.Project, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no compose file given")
	}

	files := make([]types.ConfigFile, 0, len(paths))
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", path, err)
		}
		files = append(files, types.ConfigFile{Filename: abs})
	}

	details := types.ConfigDetails{
		// Compose resolves relative paths — build contexts, env_file, volumes
		// — against the first file's directory, not against whichever file
		// mentioned them.
		WorkingDir:  filepath.Dir(files[0].Filename),
		ConfigFiles: files,
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
