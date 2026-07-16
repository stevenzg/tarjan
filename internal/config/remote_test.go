package config

import (
	"testing"
)

func TestRemoteAccessors(t *testing.T) {
	r := Remote{Host: "h"}
	if r.Target() != "h" {
		t.Errorf("Target() = %q, want h", r.Target())
	}
	if !r.ForwardEnabled() {
		t.Error("ForwardEnabled() should default to true")
	}
	if r.RemoteWorkspace("shop") != "tarjan/shop" {
		t.Errorf("RemoteWorkspace default = %q", r.RemoteWorkspace("shop"))
	}

	off := false
	r = Remote{Host: "h", User: "u", WorkspaceRoot: "/srv/ws", Forward: &off}
	if r.Target() != "u@h" {
		t.Errorf("Target() = %q, want u@h", r.Target())
	}
	if r.ForwardEnabled() {
		t.Error("ForwardEnabled() should honor forward: false")
	}
	if r.RemoteWorkspace("shop") != "/srv/ws" {
		t.Errorf("RemoteWorkspace() = %q, want /srv/ws", r.RemoteWorkspace("shop"))
	}
}

func TestServiceExtraEnvExcludesProcessEnv(t *testing.T) {
	c := &Config{Name: "p"}
	if err := c.Finalize(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FROM_PROCESS", "leak")
	spec := Service{Name: "api", Env: map[string]string{"B": "2", "A": "1"}}

	env, err := c.ServiceExtraEnv(spec)
	if err != nil {
		t.Fatalf("ServiceExtraEnv: %v", err)
	}
	// Only the configured vars, and sorted.
	want := []string{"A=1", "B=2"}
	if len(env) != len(want) {
		t.Fatalf("ServiceExtraEnv = %v, want %v", env, want)
	}
	for i := range want {
		if env[i] != want[i] {
			t.Errorf("env[%d] = %q, want %q", i, env[i], want[i])
		}
	}
	for _, kv := range env {
		if kv == "FROM_PROCESS=leak" {
			t.Error("ServiceExtraEnv leaked the process environment")
		}
	}
}

func remoteCfg(svc Service, remotes map[string]Remote) *Config {
	c := &Config{Name: "p", Remotes: remotes, Services: []Service{svc}}
	return c
}

func TestValidateRemoteReferences(t *testing.T) {
	defined := map[string]Remote{"devbox": {Host: "h"}}

	// Unknown remote reference.
	c := remoteCfg(Service{Name: "api", Command: "run", Remote: "missing"}, defined)
	if err := c.Finalize(t.TempDir()); err == nil {
		t.Error("reference to an undefined remote should fail validation")
	}

	// Valid reference.
	c = remoteCfg(Service{Name: "api", Command: "run", Remote: "devbox"}, defined)
	if err := c.Finalize(t.TempDir()); err != nil {
		t.Errorf("valid remote reference should pass: %v", err)
	}
}

func TestValidateRemoteHostRequired(t *testing.T) {
	c := remoteCfg(Service{Name: "api", Command: "run"}, map[string]Remote{"box": {}})
	if err := c.Finalize(t.TempDir()); err == nil {
		t.Error("a remote without a host should fail validation")
	}
}

// TestValidateRemoteRejectsDashHost guards the S1 fix: a host or user beginning
// with '-' would be parsed by ssh as an option (e.g. -oProxyCommand=…), so it
// must be rejected at config load — including when a cloned repo contributes it.
func TestValidateRemoteRejectsDashHost(t *testing.T) {
	dash := map[string]Remote{"box": {Host: "-oProxyCommand=touch /tmp/pwned"}}
	c := remoteCfg(Service{Name: "api", Command: "run", Remote: "box"}, dash)
	if err := c.Finalize(t.TempDir()); err == nil {
		t.Error("a remote host beginning with '-' should fail validation")
	}

	dashUser := map[string]Remote{"box": {Host: "h", User: "-oProxyCommand=x"}}
	c = remoteCfg(Service{Name: "api", Command: "run", Remote: "box"}, dashUser)
	if err := c.Finalize(t.TempDir()); err == nil {
		t.Error("a remote user beginning with '-' should fail validation")
	}
}

func TestValidateRemoteRejectsExternalAndWatch(t *testing.T) {
	defined := map[string]Remote{"box": {Host: "h"}}

	c := remoteCfg(Service{Name: "api", External: true, Remote: "box"}, defined)
	if err := c.Finalize(t.TempDir()); err == nil {
		t.Error("external + remote should fail validation")
	}

	c = remoteCfg(Service{Name: "api", Command: "run", Remote: "box", Watch: &Watch{Paths: []string{"."}}}, defined)
	if err := c.Finalize(t.TempDir()); err == nil {
		t.Error("watch + remote should fail validation")
	}
}
