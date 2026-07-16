package runner

import (
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
)

func TestRunnerRemoteFor(t *testing.T) {
	cfg := &config.Config{
		Name:    "t",
		Remotes: map[string]config.Remote{"box": {Host: "h", User: "u"}},
		Services: []config.Service{
			{Name: "r", Command: "run", Remote: "box"},
			{Name: "l", Command: "run"},
		},
	}
	r := New(cfg, t.TempDir())

	rem, ok := r.remoteFor(cfg.Services[0])
	if !ok || rem.Host != "h" {
		t.Errorf("remoteFor(remote svc) = %+v ok=%v", rem, ok)
	}
	if _, ok := r.remoteFor(cfg.Services[1]); ok {
		t.Error("remoteFor(local svc) should be false")
	}
	if _, ok := r.remoteFor(config.Service{Remote: "nope"}); ok {
		t.Error("remoteFor(unknown remote) should be false")
	}
}

func TestRunnerDockerHost(t *testing.T) {
	cfg := &config.Config{
		Name:    "t",
		Remotes: map[string]config.Remote{"box": {Host: "h", User: "u", Port: 22}},
		Services: []config.Service{
			{Name: "remote-docker", Docker: &config.DockerSpec{Image: "x"}, Remote: "box"},
			{Name: "local-docker", Docker: &config.DockerSpec{Image: "x"}},
			{Name: "remote-proc", Command: "run", Remote: "box"},
		},
	}
	r := New(cfg, t.TempDir())

	if got := r.dockerHost(cfg.Services[0]); got != "ssh://u@h:22" {
		t.Errorf("dockerHost(remote docker) = %q, want ssh://u@h:22", got)
	}
	if got := r.dockerHost(cfg.Services[1]); got != "" {
		t.Errorf("dockerHost(local docker) = %q, want empty", got)
	}
	if got := r.dockerHost(cfg.Services[2]); got != "" {
		t.Errorf("dockerHost(remote process) = %q, want empty (not docker)", got)
	}
}

func TestDockerEnviron(t *testing.T) {
	if dockerEnviron("") != nil {
		t.Error(`dockerEnviron("") should be nil so the local environment is inherited`)
	}
	env := dockerEnviron("ssh://h")
	var found bool
	for _, kv := range env {
		if kv == "DOCKER_HOST=ssh://h" {
			found = true
		}
	}
	if !found {
		t.Errorf("dockerEnviron did not set DOCKER_HOST: %v", env)
	}
}
