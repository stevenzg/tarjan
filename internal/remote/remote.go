// Package remote plans how a service runs on a named SSH target. It turns a
// config.Remote plus a service into the concrete local commands the runner
// executes: the `ssh` invocation for a process service, the DOCKER_HOST for a
// docker service on a remote daemon, the port-forward tunnels that bring the
// remote's ports back to localhost, and the remote git-clone script.
//
// Everything here is a pure function so it can be unit-tested without a live
// host. tarjan shells out to the system `ssh` and `docker` (exactly as it does
// for git), so a user's ssh config, agent, keys and known_hosts just work, and
// the binary stays dependency-free.
package remote

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/stevenzg/tarjan/internal/config"
)

// connectArgs returns the ssh connection options shared by command sessions and
// tunnels: a connect timeout and keepalives (so a dropped connection is noticed
// and the remote session is torn down), plus the remote's port, identity file
// and any extra `-o` options. The order is deterministic for testability.
func connectArgs(r config.Remote) []string {
	args := []string{
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=3",
	}
	if r.Port != 0 {
		args = append(args, "-p", strconv.Itoa(r.Port))
	}
	if r.IdentityFile != "" {
		args = append(args, "-i", r.IdentityFile)
	}
	for _, o := range r.Options {
		args = append(args, "-o", o)
	}
	return args
}

// Invocation builds the local ("ssh", args) that runs remoteScript on r. The
// script is passed as a single trailing argument, so the remote login shell
// runs it verbatim.
func Invocation(r config.Remote, remoteScript string) (string, []string) {
	args := connectArgs(r)
	args = append(args, r.Target(), remoteScript)
	return "ssh", args
}

// Script builds a POSIX shell script to run on the remote host: change into
// dir, export env, then run command. When useExec is true the command replaces
// the shell (for a long-running service, so signals reach it directly); when
// false it runs as a child (for one-shot setup steps). env entries are "K=V"
// strings, as produced by config.ServiceExtraEnv.
func Script(dir string, env []string, command string, useExec bool) string {
	var parts []string
	if dir != "" {
		parts = append(parts, "cd "+shellQuote(dir))
	}
	if len(env) > 0 {
		exports := make([]string, 0, len(env))
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			exports = append(exports, k+"="+shellQuote(v))
		}
		parts = append(parts, "export "+strings.Join(exports, " "))
	}
	run := "sh -c " + shellQuote(command)
	if useExec {
		run = "exec " + run
	}
	parts = append(parts, run)
	return strings.Join(parts, " && ")
}

// DockerHost returns the DOCKER_HOST value that points the docker CLI at r's
// daemon over SSH: "ssh://[user@]host[:port]".
func DockerHost(r config.Remote) string {
	h := "ssh://" + r.Target()
	if r.Port != 0 {
		h += ":" + strconv.Itoa(r.Port)
	}
	return h
}

// TunnelArgs builds the local ("ssh", args) for a forward-only session that
// tunnels each port back from the remote to the same localhost port. It returns
// ok=false when there are no ports to forward. ExitOnForwardFailure makes ssh
// exit (rather than sit idle) if a local port is already taken, so the runner
// can surface the clash.
func TunnelArgs(r config.Remote, ports []int) (name string, args []string, ok bool) {
	if len(ports) == 0 {
		return "", nil, false
	}
	args = connectArgs(r)
	args = append(args, "-N", "-o", "ExitOnForwardFailure=yes")
	for _, p := range ports {
		args = append(args, "-L", fmt.Sprintf("%d:localhost:%d", p, p))
	}
	args = append(args, r.Target())
	return "ssh", args, true
}

// CloneScript builds a remote POSIX script that clones repo into
// <workspaceRoot>/<repo path>, skipping the clone if the checkout already
// exists. It mirrors the local clone hardening (blocking git's ext:: transport
// and terminating flags with --).
func CloneScript(workspaceRoot string, repo config.Repo) string {
	dest := WorkdirPath(workspaceRoot, repo.Path())
	parent := path.Dir(dest)
	clone := []string{"git", "-c", "protocol.ext.allow=never", "clone", "--depth", "1"}
	if repo.Branch != "" {
		clone = append(clone, "--branch", shellQuote(repo.Branch))
	}
	clone = append(clone, "--", shellQuote(repo.URL), shellQuote(dest))
	return fmt.Sprintf("mkdir -p %s && { test -d %s/.git || %s; }",
		shellQuote(parent), shellQuote(dest), strings.Join(clone, " "))
}

// WorkdirPath joins a remote workspace root and a service workdir using forward
// slashes (the remote is POSIX). An empty workdir resolves to the root.
func WorkdirPath(workspaceRoot, workdir string) string {
	if workdir == "" {
		return workspaceRoot
	}
	return path.Join(workspaceRoot, workdir)
}

// ForwardPorts returns the ports to tunnel for a remote service, sorted and
// de-duplicated: the host ports of a docker service's published ports, plus the
// localhost port of its health check (if any). These are the ports a local
// dependent or health probe expects to reach at localhost.
func ForwardPorts(spec config.Service) []int {
	seen := map[int]bool{}
	if spec.Docker != nil {
		for _, p := range spec.Docker.Ports {
			if hp := hostPort(p); hp != 0 {
				seen[hp] = true
			}
		}
	}
	if hp := HealthPort(spec.Health); hp != 0 {
		seen[hp] = true
	}
	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// HealthPort extracts the localhost port a health probe targets, or 0 when the
// probe is not a localhost tcp/http check. It is what makes an auto-tunnel line
// up with the port a service's dependents wait on.
func HealthPort(h *config.Health) int {
	if h == nil {
		return 0
	}
	switch {
	case h.TCP != "":
		host, port, err := net.SplitHostPort(h.TCP)
		if err != nil || !isLocalhost(host) {
			return 0
		}
		n, _ := strconv.Atoi(port)
		return n
	case h.HTTP != "":
		u, err := url.Parse(h.HTTP)
		if err != nil || !isLocalhost(u.Hostname()) {
			return 0
		}
		if p := u.Port(); p != "" {
			n, _ := strconv.Atoi(p)
			return n
		}
		if u.Scheme == "https" {
			return 443
		}
		return 80
	default:
		return 0
	}
}

// hostPort parses the published host port from a docker "-p" mapping
// ("host:container", "ip:host:container", or bare "container"). It returns 0
// for a bare container port (Docker assigns a random host port that cannot be
// forwarded deterministically) or a non-numeric mapping (e.g. a port range).
func hostPort(mapping string) int {
	spec := mapping
	if i := strings.IndexByte(spec, '/'); i >= 0 {
		spec = spec[:i] // drop a "/tcp" or "/udp" suffix
	}
	fields := strings.Split(spec, ":")
	var host string
	switch len(fields) {
	case 2:
		host = fields[0]
	case 3:
		host = fields[1]
	default:
		return 0 // bare container port, or an unexpected shape
	}
	n, err := strconv.Atoi(host)
	if err != nil {
		return 0
	}
	return n
}

func isLocalhost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// shellQuote wraps s in single quotes for a POSIX shell, escaping any embedded
// single quotes. It makes command strings, paths and env values that contain
// spaces or metacharacters safe to embed in a remote script.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
