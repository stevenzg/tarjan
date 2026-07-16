// Package repocfg discovers and merges per-repo tarjan configs. A repository
// can carry its own config in a .tarjan directory (.tarjan/tarjan.star, .yaml
// or .yml) describing how to run it; when a parent config clones such a repo,
// the repo's tools and services are folded into the parent's run, with paths
// (workdir, docker build context, env files) rebased onto the repo's checkout
// inside the workspace.
package repocfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stevenzg/tarjan/internal/config"
	"github.com/stevenzg/tarjan/internal/starcfg"
)

// candidates are the config file names tried, in order, inside a repo's
// .tarjan directory.
var candidates = []string{"tarjan.star", "tarjan.yaml", "tarjan.yml"}

// Find returns the path of the repo's .tarjan config file, or "" when the
// repo carries none.
func Find(repoDir string) string {
	for _, c := range candidates {
		p := filepath.Join(repoDir, config.RepoConfigDir, c)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// Load reads a repo's .tarjan config as an unvalidated fragment, dispatching
// by extension like the top-level loader.
func Load(path string) (*config.Config, error) {
	if strings.HasSuffix(path, ".star") {
		return starcfg.LoadFragment(path)
	}
	return config.LoadFragment(path)
}

// Merged describes what one repo's .tarjan config contributed to the run.
type Merged struct {
	// Repo is the repo's name in the parent config.
	Repo string
	// Path is the repo config file that was loaded.
	Path string
	// Services are the names of the services added to the parent config.
	Services []string
	// Tools are the required tools added to the parent config.
	Tools []config.Tool
	// Skipped are service names the repo declared but the parent (or an
	// earlier repo) already defines; the existing definition wins, so a parent
	// config can override a repo's defaults.
	Skipped []string
	// SkippedTools describes required tools the repo re-declared with a different
	// minVersion than an already-required entry: the existing one wins, and this
	// records the conflict so it isn't lost silently.
	SkippedTools []string
}

// Summary aggregates the per-repo merges of one Apply call.
type Summary struct {
	Merged []Merged
}

// Tools returns every tool added across all merged repo configs, for a
// follow-up dependency check.
func (s *Summary) Tools() []config.Tool {
	var out []config.Tool
	for _, m := range s.Merged {
		out = append(out, m.Tools...)
	}
	return out
}

// Apply discovers the .tarjan config of each cloned repo under wsDir, merges
// their tools and services into base, and re-validates the combined config.
// Repos without a .tarjan config are skipped. The returned summary reports
// what each repo contributed.
func Apply(base *config.Config, wsDir string, repos []config.Repo) (*Summary, error) {
	sum := &Summary{}
	for _, repo := range repos {
		// Absolute so rewritten env-file paths don't get re-resolved against
		// the parent config's directory by ServiceEnv.
		repoDir, err := filepath.Abs(filepath.Join(wsDir, repo.Path()))
		if err != nil {
			return nil, err
		}
		path := Find(repoDir)
		if path == "" {
			continue
		}
		frag, err := Load(path)
		if err != nil {
			return nil, fmt.Errorf("repo %s: %w", repo.Name, err)
		}
		m := merge(base, repoDir, repo, frag)
		m.Path = path
		sum.Merged = append(sum.Merged, m)
	}
	if len(sum.Merged) == 0 {
		return sum, nil
	}
	if err := base.Validate(); err != nil {
		return nil, fmt.Errorf("after merging repo configs: %w", err)
	}
	return sum, nil
}

// merge folds one repo fragment into base. Service paths are rebased onto the
// repo's checkout: workdir and docker build contexts get the repo path
// prefixed (an empty workdir becomes the repo root), and relative env files
// resolve against the repo directory.
func merge(base *config.Config, repoDir string, repo config.Repo, frag *config.Config) Merged {
	m := Merged{Repo: repo.Name}

	tools := map[string]config.Tool{}
	for _, t := range base.Requires {
		tools[t.Name] = t
	}
	for _, t := range frag.Requires {
		if ex, ok := tools[t.Name]; ok {
			// Already required by the parent or an earlier repo; the existing
			// entry wins. If this repo asks for a different minVersion, that
			// intent is being dropped — record it so the caller can warn.
			if t.MinVersion != "" && t.MinVersion != ex.MinVersion {
				had := ex.MinVersion
				if had == "" {
					had = "unversioned"
				}
				m.SkippedTools = append(m.SkippedTools,
					fmt.Sprintf("%s (wanted %s, keeping %s)", t.Name, t.MinVersion, had))
			}
			continue
		}
		tools[t.Name] = t
		base.Requires = append(base.Requires, t)
		m.Tools = append(m.Tools, t)
	}

	// Fold in the fragment's remotes so a repo service's `remote:` reference
	// resolves after merge. The parent wins on a name clash, matching services.
	for name, r := range frag.Remotes {
		if _, ok := base.Remotes[name]; ok {
			continue
		}
		if base.Remotes == nil {
			base.Remotes = map[string]config.Remote{}
		}
		base.Remotes[name] = r
	}

	existing := map[string]bool{}
	for _, s := range base.Services {
		existing[s.Name] = true
	}
	for _, svc := range frag.Services {
		if existing[svc.Name] {
			m.Skipped = append(m.Skipped, svc.Name)
			continue
		}
		existing[svc.Name] = true
		base.Services = append(base.Services, rebaseService(svc, repoDir, repo.Path(), frag.EnvFile))
		m.Services = append(m.Services, svc.Name)
	}
	return m
}

// rebaseService rewrites one fragment service so it runs against the repo's
// checkout when started from the workspace root.
func rebaseService(svc config.Service, repoDir, repoPath string, globalEnvFiles []string) config.Service {
	if !filepath.IsAbs(svc.Workdir) {
		svc.Workdir = filepath.Join(repoPath, svc.Workdir)
	}
	if svc.Docker != nil && svc.Docker.Build != nil {
		b := *svc.Docker.Build
		if !filepath.IsAbs(b.Context) {
			b.Context = filepath.Join(repoPath, b.Context)
		}
		d := *svc.Docker
		d.Build = &b
		svc.Docker = &d
	}
	// The fragment's global env files apply to each of its services; layer
	// them before the service's own, preserving the fragment's precedence.
	files := make([]string, 0, len(globalEnvFiles)+len(svc.EnvFile))
	files = append(files, globalEnvFiles...)
	files = append(files, svc.EnvFile...)
	if len(files) > 0 {
		abs := make([]string, len(files))
		for i, f := range files {
			if filepath.IsAbs(f) {
				abs[i] = f
			} else {
				abs[i] = filepath.Join(repoDir, f)
			}
		}
		svc.EnvFile = abs
	}
	return svc
}
