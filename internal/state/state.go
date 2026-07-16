// Package state persists what an environment is running (PIDs, docker
// containers) so `tarjan down` and `tarjan status` can act on a workspace started
// by an earlier process.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const fileName = ".tarjan/state.json"

// State is the on-disk record of a running environment.
type State struct {
	Name      string    `json:"name"`
	Workspace string    `json:"workspace"`
	StartedAt time.Time `json:"startedAt"`
	Services  []Service `json:"services"`
}

// Service records one running service instance.
type Service struct {
	Name      string `json:"name"`
	PID       int    `json:"pid,omitempty"`       // local process group leader
	Container string `json:"container,omitempty"` // docker container name
	Docker    bool   `json:"docker"`
	External  bool   `json:"external,omitempty"` // cloud/remote dependency
	Job       bool   `json:"job,omitempty"`      // run-to-completion job
	Remote    string `json:"remote,omitempty"`   // name of the remote it runs on
}

// Path returns the state file path for a workspace.
func Path(workspace string) string {
	return filepath.Join(workspace, fileName)
}

// Save writes the state to the workspace atomically: it writes a temp file in
// the same directory and renames it over the target, so a crash or kill
// mid-write can never leave a truncated or empty state.json behind (which would
// leave `down`/`status` unable to find the live PIDs and containers).
func Save(workspace string, s *State) error {
	dir := filepath.Join(workspace, ".tarjan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// 0600: the state file records PIDs that `down` signals and container names
	// it removes. Keeping it owner-only stops another local user from injecting
	// a PID/container for tarjan to act on, and avoids leaking the workspace
	// layout.
	tmp, err := os.CreateTemp(dir, "state-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, Path(workspace))
}

// Load reads the state from a workspace.
func Load(workspace string) (*State, error) {
	data, err := os.ReadFile(Path(workspace))
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Remove deletes the state file (best effort).
func Remove(workspace string) {
	_ = os.Remove(Path(workspace))
}
