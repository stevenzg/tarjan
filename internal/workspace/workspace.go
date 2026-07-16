// Package workspace manages the per-run workspace directory and generates the
// VS Code multi-root workspace file so every cloned repo opens in one window.
package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stevenzg/tarjan/internal/config"
)

// lastPointer is the file (under WorkspaceRoot) recording the most recent
// workspace, so `tarjan down`/`status` can find it without arguments.
const lastPointer = ".last"

// Materialize returns the workspace directory for a run and ensures it exists,
// recording it as the most recent one. With a non-empty version it is a stable,
// reusable directory named "<name>-<version>" under WorkspaceRoot — so repeated
// `tarjan up` runs of the same version reuse the same checkout (cloned repos and
// completed setup steps are skipped). With an empty version it falls back to a
// fresh, timestamped directory. reused reports whether the directory already
// existed. The stamp is supplied by the caller to keep this deterministic.
func Materialize(cfg *config.Config, version string, stamp time.Time) (dir string, reused bool, err error) {
	if v := sanitizeVersion(version); v != "" {
		dir = filepath.Join(cfg.WorkspaceRoot, cfg.Name+"-"+v)
	} else {
		dir = filepath.Join(cfg.WorkspaceRoot, stamp.Format("20060102-150405"))
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		reused = true
	}
	// MkdirAll creates dir and its .tarjan subdir, and is a no-op when reusing.
	if err = os.MkdirAll(filepath.Join(dir, ".tarjan"), 0o755); err != nil {
		return "", false, err
	}
	if err = setLast(cfg.WorkspaceRoot, dir); err != nil {
		return "", false, err
	}
	return dir, reused, nil
}

// Create returns a fresh, timestamped workspace directory — the version-less
// form of Materialize, kept for callers that don't use versioned workspaces.
func Create(cfg *config.Config, stamp time.Time) (string, error) {
	dir, _, err := Materialize(cfg, "", stamp)
	return dir, err
}

// VersionDir returns the path of the named, reusable workspace for a version
// label — "<name>-<version>" under WorkspaceRoot — without creating it. It
// mirrors the path Materialize builds, so `tarjan up --version <v>` and a
// command that later addresses that workspace by version (e.g. `tarjan pull
// <v>`) agree on the same directory.
func VersionDir(cfg *config.Config, version string) string {
	return filepath.Join(cfg.WorkspaceRoot, cfg.Name+"-"+sanitizeVersion(version))
}

// sanitizeVersion makes a version label safe to use as a single path segment,
// collapsing separators/whitespace to dashes.
func sanitizeVersion(v string) string {
	v = strings.TrimSpace(v)
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', ' ', '\t':
			return '-'
		}
		return r
	}, v)
}

// Resolve returns an explicit workspace if provided; for a config loaded from
// a repo's own .tarjan directory the repo checkout is the workspace; otherwise
// it is the most recent one recorded under WorkspaceRoot.
func Resolve(cfg *config.Config, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if dir := cfg.InPlaceDir(); dir != "" {
		return dir, nil
	}
	last, err := getLast(cfg.WorkspaceRoot)
	if err != nil {
		return "", fmt.Errorf("no workspace found under %s (run `tarjan up` first): %w", cfg.WorkspaceRoot, err)
	}
	return last, nil
}

func setLast(root, dir string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, lastPointer), []byte(dir), 0o644)
}

func getLast(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, lastPointer))
	if err != nil {
		return "", err
	}
	dir := string(b)
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// vscodeWorkspace mirrors the .code-workspace JSON schema (the subset we emit).
type vscodeWorkspace struct {
	Folders  []vscodeFolder `json:"folders"`
	Settings map[string]any `json:"settings,omitempty"`
}

type vscodeFolder struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// WriteVSCode generates "<name>.code-workspace" in the workspace root, listing
// the given repos as folders. Returns the file path.
func WriteVSCode(cfg *config.Config, workspaceDir string, repos []config.Repo) (string, error) {
	ws := vscodeWorkspace{Settings: map[string]any{}}
	for _, r := range repos {
		ws.Folders = append(ws.Folders, vscodeFolder{
			Name: r.Name,
			Path: r.Path(),
		})
	}
	data, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(workspaceDir, cfg.Name+".code-workspace")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
