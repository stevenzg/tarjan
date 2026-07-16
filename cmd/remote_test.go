package cmd

import (
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
)

func TestReposForRemote(t *testing.T) {
	repos := []config.Repo{
		{Name: "api", URL: "u1"},
		{Name: "web", URL: "u2", Dir: "front/web"},
		{Name: "docs", URL: "u3"},
	}
	services := []config.Service{
		{Name: "api", Remote: "box", Workdir: "api", Command: "run"},          // matches repo api
		{Name: "web", Remote: "box", Workdir: "front/web", Command: "r"},      // matches repo web (custom dir)
		{Name: "worker", Remote: "box", Workdir: "api/worker"},                // nested → still repo api
		{Name: "local", Workdir: "docs"},                                      // not remote → ignored
		{Name: "cont", Remote: "box", Docker: &config.DockerSpec{Image: "x"}}, // docker → ignored
		{Name: "root", Remote: "box", Workdir: ""},                            // no workdir → no repo
	}

	got := reposForRemote("box", services, repos)
	names := map[string]bool{}
	for _, r := range got {
		names[r.Name] = true
	}
	if !names["api"] || !names["web"] {
		t.Errorf("expected api and web, got %v", names)
	}
	if names["docs"] {
		t.Error("docs is only used by a local service; must not be cloned remotely")
	}
	if len(got) != 2 {
		t.Errorf("got %d repos, want 2", len(got))
	}
}

func TestReposForRemoteOtherRemote(t *testing.T) {
	repos := []config.Repo{{Name: "api", URL: "u"}}
	services := []config.Service{{Name: "api", Remote: "other", Workdir: "api", Command: "run"}}
	if got := reposForRemote("box", services, repos); len(got) != 0 {
		t.Errorf("services on a different remote must not match: %v", got)
	}
}
