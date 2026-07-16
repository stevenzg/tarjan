package remote

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
)

func TestInvocation(t *testing.T) {
	r := config.Remote{Host: "dev.example.com", User: "steven", Port: 2222, IdentityFile: "/k/id", Options: []string{"StrictHostKeyChecking=accept-new"}}
	name, args := Invocation(r, "echo hi")
	if name != "ssh" {
		t.Fatalf("name = %q, want ssh", name)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-p 2222", "-i /k/id", "-o StrictHostKeyChecking=accept-new", "steven@dev.example.com", "ConnectTimeout=10"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
	// The remote script must be the final single argument, unsplit.
	if args[len(args)-1] != "echo hi" {
		t.Errorf("last arg = %q, want the script", args[len(args)-1])
	}
	// The target must come immediately before the script.
	if args[len(args)-2] != "steven@dev.example.com" {
		t.Errorf("penultimate arg = %q, want target", args[len(args)-2])
	}
}

func TestInvocationMinimal(t *testing.T) {
	// No user/port/identity/options: only the shared connect options + host.
	_, args := Invocation(config.Remote{Host: "box"}, "run")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-p ") || strings.Contains(joined, "-i ") {
		t.Errorf("unexpected -p/-i in %q", joined)
	}
	if args[len(args)-2] != "box" {
		t.Errorf("target = %q, want box", args[len(args)-2])
	}
}

func TestScript(t *testing.T) {
	got := Script("api/src", []string{"PORT=8080", "MSG=a b"}, "npm run dev", true)
	want := `cd 'api/src' && export PORT='8080' MSG='a b' && exec sh -c 'npm run dev'`
	if got != want {
		t.Errorf("Script:\n got %q\nwant %q", got, want)
	}
}

func TestScriptNoExecNoDirNoEnv(t *testing.T) {
	got := Script("", nil, "make setup", false)
	if got != `sh -c 'make setup'` {
		t.Errorf("Script = %q", got)
	}
}

func TestScriptQuotesSingleQuotes(t *testing.T) {
	got := Script("", nil, "echo 'hi'", true)
	want := `exec sh -c 'echo '\''hi'\'''`
	if got != want {
		t.Errorf("Script single-quote escaping:\n got %q\nwant %q", got, want)
	}
}

func TestDockerHost(t *testing.T) {
	cases := []struct {
		r    config.Remote
		want string
	}{
		{config.Remote{Host: "h"}, "ssh://h"},
		{config.Remote{Host: "h", User: "u"}, "ssh://u@h"},
		{config.Remote{Host: "h", User: "u", Port: 22}, "ssh://u@h:22"},
	}
	for _, c := range cases {
		if got := DockerHost(c.r); got != c.want {
			t.Errorf("DockerHost(%+v) = %q, want %q", c.r, got, c.want)
		}
	}
}

func TestTunnelArgs(t *testing.T) {
	name, args, ok := TunnelArgs(config.Remote{Host: "box", User: "u"}, []int{5432, 8080})
	if !ok || name != "ssh" {
		t.Fatalf("ok=%v name=%q", ok, name)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-N", "ExitOnForwardFailure=yes", "-L 5432:localhost:5432", "-L 8080:localhost:8080"} {
		if !strings.Contains(joined, want) {
			t.Errorf("tunnel args %q missing %q", joined, want)
		}
	}
	if args[len(args)-1] != "u@box" {
		t.Errorf("target must be last, got %q", args[len(args)-1])
	}
}

func TestTunnelArgsEmpty(t *testing.T) {
	if _, _, ok := TunnelArgs(config.Remote{Host: "box"}, nil); ok {
		t.Error("TunnelArgs with no ports should report ok=false")
	}
}

func TestCloneScript(t *testing.T) {
	got := CloneScript("tarjan/shop", config.Repo{Name: "api", URL: "https://git/api.git", Branch: "main"})
	for _, want := range []string{
		"mkdir -p 'tarjan/shop'",
		"test -d 'tarjan/shop/api'/.git",
		"protocol.ext.allow=never",
		"--branch 'main'",
		"-- 'https://git/api.git' 'tarjan/shop/api'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CloneScript missing %q in:\n%s", want, got)
		}
	}
}

func TestCloneScriptCustomDirNoBranch(t *testing.T) {
	got := CloneScript("root", config.Repo{Name: "web", URL: "u", Dir: "front/web"})
	if !strings.Contains(got, "'root/front/web'") {
		t.Errorf("custom dir not honored: %s", got)
	}
	if strings.Contains(got, "--branch") {
		t.Errorf("no branch should mean no --branch flag: %s", got)
	}
}

func TestWorkdirPath(t *testing.T) {
	if got := WorkdirPath("root", ""); got != "root" {
		t.Errorf("empty workdir = %q, want root", got)
	}
	if got := WorkdirPath("root", "a/b"); got != "root/a/b" {
		t.Errorf("got %q, want root/a/b", got)
	}
}

func TestHealthPort(t *testing.T) {
	cases := []struct {
		h    *config.Health
		want int
	}{
		{nil, 0},
		{&config.Health{TCP: "localhost:5432"}, 5432},
		{&config.Health{TCP: "127.0.0.1:6000"}, 6000},
		{&config.Health{TCP: "db.example.com:5432"}, 0}, // not localhost
		{&config.Health{HTTP: "http://localhost:8080/health"}, 8080},
		{&config.Health{HTTP: "https://localhost/health"}, 443},
		{&config.Health{HTTP: "http://localhost/health"}, 80},
		{&config.Health{HTTP: "http://api.example.com:8080/x"}, 0},
		{&config.Health{Command: "true"}, 0},
	}
	for _, c := range cases {
		if got := HealthPort(c.h); got != c.want {
			t.Errorf("HealthPort(%+v) = %d, want %d", c.h, got, c.want)
		}
	}
}

func TestForwardPorts(t *testing.T) {
	spec := config.Service{
		Docker: &config.DockerSpec{Ports: []string{"5432:5432", "127.0.0.1:6000:6000", "9999", "3000-3002:3000-3002"}},
		Health: &config.Health{TCP: "localhost:8080"},
	}
	got := ForwardPorts(spec)
	want := []int{5432, 6000, 8080}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ForwardPorts = %v, want %v", got, want)
	}
}

func TestForwardPortsProcessService(t *testing.T) {
	got := ForwardPorts(config.Service{Command: "run", Health: &config.Health{HTTP: "http://localhost:8080/"}})
	if !reflect.DeepEqual(got, []int{8080}) {
		t.Errorf("ForwardPorts = %v, want [8080]", got)
	}
}

func TestForwardPortsDedup(t *testing.T) {
	// Health port equal to a published docker host port must not double up.
	spec := config.Service{
		Docker: &config.DockerSpec{Ports: []string{"8080:80"}},
		Health: &config.Health{HTTP: "http://localhost:8080/"},
	}
	if got := ForwardPorts(spec); !reflect.DeepEqual(got, []int{8080}) {
		t.Errorf("ForwardPorts = %v, want [8080]", got)
	}
}
