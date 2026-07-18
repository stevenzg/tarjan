// Package starcfg loads a tarjan config written in Starlark (tarjan.star) — a small,
// sandboxed Python-like language. It gives configs loops, conditionals,
// functions and computed values without a heavy runtime: the interpreter is
// pure Go, so tarjan stays a single binary. A tarjan.star produces the very same
// config.Config a tarjan.yaml would, so every downstream feature works unchanged.
//
// A tarjan.star must assign its result to a top-level `tarjan`:
//
//	services = [service(name = "db", docker = docker(image = "postgres:16"))]
//	for i, n in enumerate(["a", "b", "c"]):
//	    services.append(service(name = n, command = "go run ./" + n,
//	                            env = {"PORT": str(8000 + i)}, depends_on = ["db"]))
//	tarjan = config(name = "demo", services = services)
package starcfg

import (
	"fmt"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/stevenzg/tarjan/internal/config"
)

// fileOptions enables the Python-like conveniences a config author expects:
// top-level for/if, while loops, set literals, global reassignment.
var fileOptions = &syntax.FileOptions{
	Set:             true,
	While:           true,
	TopLevelControl: true,
	GlobalReassign:  true,
	Recursion:       true,
}

// Load executes a tarjan.star file and returns the finalized config.
func Load(path string) (*config.Config, error) {
	return loadFile(path, true)
}

// LoadFragment executes a per-repo tarjan.star fragment, skipping structural
// validation: a fragment may reference services that only exist once it is
// merged into its parent config, which is validated as a whole afterwards.
func LoadFragment(path string) (*config.Config, error) {
	return loadFile(path, false)
}

func loadFile(path string, validate bool) (*config.Config, error) {
	thread := &starlark.Thread{Name: "tarjan"}
	globals, err := starlark.ExecFileOptions(fileOptions, thread, path, nil, builtins())
	if err != nil {
		return nil, err // Starlark errors carry a useful backtrace
	}
	v, ok := globals["tarjan"]
	if !ok {
		return nil, fmt.Errorf("%s must assign a top-level `tarjan = config(...)`", path)
	}
	w, ok := v.(wrapped)
	if !ok || w.typeName != "config" {
		return nil, fmt.Errorf("`tarjan` must be a config(...) value, got %s", v.Type())
	}
	cfg := w.v.(*config.Config)
	dir, inPlace, err := config.ResolveDir(path)
	if err != nil {
		return nil, err
	}
	cfg.SetInPlaceDir(inPlace)
	if !validate {
		if err := cfg.FinalizeFragment(dir); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err := cfg.Finalize(dir); err != nil {
		return nil, err
	}
	return cfg, nil
}

// wrapped adapts a Go config value into a Starlark value so builtins can pass
// structured objects (repos, services, …) to each other.
type wrapped struct {
	typeName string
	v        any
}

func (w wrapped) String() string        { return "tarjan." + w.typeName }
func (w wrapped) Type() string          { return "tarjan." + w.typeName }
func (w wrapped) Freeze()               {}
func (w wrapped) Truth() starlark.Bool  { return starlark.True }
func (w wrapped) Hash() (uint32, error) { return 0, fmt.Errorf("tarjan.%s is unhashable", w.typeName) }

func builtins() starlark.StringDict {
	return starlark.StringDict{
		"config":    starlark.NewBuiltin("config", bConfig),
		"repo":      starlark.NewBuiltin("repo", bRepo),
		"tool":      starlark.NewBuiltin("tool", bTool),
		"service":   starlark.NewBuiltin("service", bService),
		"docker":    starlark.NewBuiltin("docker", bDocker),
		"health":    starlark.NewBuiltin("health", bHealth),
		"watch":     starlark.NewBuiltin("watch", bWatch),
		"hooks":     starlark.NewBuiltin("hooks", bHooks),
		"remote":    starlark.NewBuiltin("remote", bRemote),
		"workspace": starlark.NewBuiltin("workspace", bWorkspace),
	}
}

func bRepo(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name, url, branch, dir string
	var profiles starlark.Value
	if err := starlark.UnpackArgs("repo", args, kwargs,
		"name", &name, "url", &url, "branch?", &branch, "dir?", &dir, "profiles?", &profiles); err != nil {
		return nil, err
	}
	p, err := toStrings("profiles", profiles)
	if err != nil {
		return nil, err
	}
	return wrapped{"repo", config.Repo{Name: name, URL: url, Branch: branch, Dir: dir, Profiles: p}}, nil
}

func bTool(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name, minVersion, versionCommand, installHint, mise, check string
	var optional bool
	var install, pkg, services starlark.Value
	if err := starlark.UnpackArgs("tool", args, kwargs,
		"name", &name, "min_version?", &minVersion, "version_command?", &versionCommand,
		"install?", &install, "mise?", &mise, "package?", &pkg,
		"install_hint?", &installHint, "optional?", &optional, "services?", &services,
		"check?", &check); err != nil {
		return nil, err
	}
	spec, err := toInstall("install", install)
	if err != nil {
		return nil, err
	}
	pkgSpec, err := toPackage("package", pkg)
	if err != nil {
		return nil, err
	}
	svc, err := toStrings("services", services)
	if err != nil {
		return nil, err
	}
	return wrapped{"tool", config.Tool{
		Name: name, MinVersion: minVersion, VersionCommand: versionCommand,
		Install: spec, Mise: mise, Package: pkgSpec,
		InstallHint: installHint, Optional: optional, Services: svc, Check: check,
	}}, nil
}

func bDocker(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var image, build, dockerfile string
	var ports, env, volumes, extra, buildArgs, command starlark.Value
	if err := starlark.UnpackArgs("docker", args, kwargs,
		"image?", &image, "build?", &build, "dockerfile?", &dockerfile,
		"build_args?", &buildArgs, "ports?", &ports, "env?", &env,
		"volumes?", &volumes, "args?", &extra, "command?", &command); err != nil {
		return nil, err
	}
	d := &config.DockerSpec{Image: image}
	var err error
	if build != "" {
		d.Build = &config.DockerBuild{Context: build, Dockerfile: dockerfile}
		if d.Build.Args, err = toStrMap("build_args", buildArgs); err != nil {
			return nil, err
		}
	}
	if d.Ports, err = toStrings("ports", ports); err != nil {
		return nil, err
	}
	if d.Env, err = toStrMap("env", env); err != nil {
		return nil, err
	}
	if d.Volumes, err = toStrings("volumes", volumes); err != nil {
		return nil, err
	}
	if d.Args, err = toStrings("args", extra); err != nil {
		return nil, err
	}
	if d.Command, err = toStrings("command", command); err != nil {
		return nil, err
	}
	return wrapped{"docker", d}, nil
}

func bHealth(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var tcp, http, command, timeout, interval string
	if err := starlark.UnpackArgs("health", args, kwargs,
		"tcp?", &tcp, "http?", &http, "command?", &command, "timeout?", &timeout, "interval?", &interval); err != nil {
		return nil, err
	}
	return wrapped{"health", &config.Health{TCP: tcp, HTTP: http, Command: command, Timeout: timeout, Interval: interval}}, nil
}

func bWatch(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var paths starlark.Value
	var debounce string
	if err := starlark.UnpackArgs("watch", args, kwargs, "paths", &paths, "debounce?", &debounce); err != nil {
		return nil, err
	}
	p, err := toStrings("paths", paths)
	if err != nil {
		return nil, err
	}
	return wrapped{"watch", &config.Watch{Paths: p, Debounce: debounce}}, nil
}

func bHooks(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var preUp, postDown starlark.Value
	if err := starlark.UnpackArgs("hooks", args, kwargs, "pre_up?", &preUp, "post_down?", &postDown); err != nil {
		return nil, err
	}
	pre, err := toStrings("pre_up", preUp)
	if err != nil {
		return nil, err
	}
	post, err := toStrings("post_down", postDown)
	if err != nil {
		return nil, err
	}
	return wrapped{"hooks", config.Hooks{PreUp: pre, PostDown: post}}, nil
}

func bRemote(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var host, user, identityFile, workspaceRoot string
	var port int
	var options, forward starlark.Value
	if err := starlark.UnpackArgs("remote", args, kwargs,
		"host", &host, "user?", &user, "port?", &port, "identity_file?", &identityFile,
		"workspace_root?", &workspaceRoot, "options?", &options, "forward?", &forward); err != nil {
		return nil, err
	}
	r := config.Remote{Host: host, User: user, Port: port, IdentityFile: identityFile, WorkspaceRoot: workspaceRoot}
	var err error
	if r.Options, err = toStrings("options", options); err != nil {
		return nil, err
	}
	// forward defaults to true; only a supplied bool overrides it.
	if !isNone(forward) {
		b, ok := forward.(starlark.Bool)
		if !ok {
			return nil, fmt.Errorf("forward: expected a bool, got %s", forward.Type())
		}
		v := bool(b)
		r.Forward = &v
	}
	return wrapped{"remote", r}, nil
}

func bWorkspace(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var vscode bool
	if err := starlark.UnpackArgs("workspace", args, kwargs, "vscode?", &vscode); err != nil {
		return nil, err
	}
	return wrapped{"workspace", config.Workspace{VSCode: vscode}}, nil
}

func bService(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name, kind, workdir, command, setupCheck, restart, remoteName string
	var external, optional bool
	var setup, env, envFile, dependsOn, profiles, dockerV, healthV, watchV, maxRestarts starlark.Value
	if err := starlark.UnpackArgs("service", args, kwargs,
		"name", &name, "kind?", &kind, "workdir?", &workdir, "command?", &command,
		"setup?", &setup, "setup_check?", &setupCheck, "env?", &env, "env_file?", &envFile, "depends_on?", &dependsOn,
		"docker?", &dockerV, "external?", &external, "health?", &healthV, "optional?", &optional,
		"restart?", &restart, "max_restarts?", &maxRestarts, "watch?", &watchV, "profiles?", &profiles,
		"remote?", &remoteName,
	); err != nil {
		return nil, err
	}

	s := config.Service{Name: name, Kind: kind, Workdir: workdir, Command: command, SetupCheck: setupCheck, External: external, Optional: optional, Restart: restart, Remote: remoteName}
	var err error
	if s.Setup, err = toStrings("setup", setup); err != nil {
		return nil, err
	}
	if s.Env, err = toStrMap("env", env); err != nil {
		return nil, err
	}
	if s.EnvFile, err = toStrings("env_file", envFile); err != nil {
		return nil, err
	}
	if s.DependsOn, err = toStrings("depends_on", dependsOn); err != nil {
		return nil, err
	}
	if s.Profiles, err = toStrings("profiles", profiles); err != nil {
		return nil, err
	}
	if d, err := optWrapped("docker", "docker", dockerV); err != nil {
		return nil, err
	} else if d != nil {
		s.Docker = d.(*config.DockerSpec)
	}
	if h, err := optWrapped("health", "health", healthV); err != nil {
		return nil, err
	} else if h != nil {
		s.Health = h.(*config.Health)
	}
	if wv, err := optWrapped("watch", "watch", watchV); err != nil {
		return nil, err
	} else if wv != nil {
		s.Watch = wv.(*config.Watch)
	}
	if n, ok, err := optInt("max_restarts", maxRestarts); err != nil {
		return nil, err
	} else if ok {
		s.MaxRestarts = &n
	}
	return wrapped{"service", s}, nil
}

func bConfig(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name, version, workspaceRoot string
	var envFile, requires, repos, services, workspaceV, hooksV, remotesV starlark.Value
	if err := starlark.UnpackArgs("config", args, kwargs,
		"name?", &name, "version?", &version, "workspace_root?", &workspaceRoot, "env_file?", &envFile,
		"requires?", &requires, "repos?", &repos, "services?", &services,
		"workspace?", &workspaceV, "hooks?", &hooksV, "remotes?", &remotesV,
	); err != nil {
		return nil, err
	}

	cfg := &config.Config{Name: name, Version: version, WorkspaceRoot: workspaceRoot}
	var err error
	if cfg.EnvFile, err = toStrings("env_file", envFile); err != nil {
		return nil, err
	}
	if cfg.Remotes, err = toRemotes("remotes", remotesV); err != nil {
		return nil, err
	}
	if err := unwrapInto("requires", "tool", requires, func(v any) { cfg.Requires = append(cfg.Requires, v.(config.Tool)) }); err != nil {
		return nil, err
	}
	if err := unwrapInto("repos", "repo", repos, func(v any) { cfg.Repos = append(cfg.Repos, v.(config.Repo)) }); err != nil {
		return nil, err
	}
	if err := unwrapInto("services", "service", services, func(v any) { cfg.Services = append(cfg.Services, v.(config.Service)) }); err != nil {
		return nil, err
	}
	if wv, err := optWrapped("workspace", "workspace", workspaceV); err != nil {
		return nil, err
	} else if wv != nil {
		cfg.Workspace = wv.(config.Workspace)
	}
	if hv, err := optWrapped("hooks", "hooks", hooksV); err != nil {
		return nil, err
	} else if hv != nil {
		cfg.Hooks = hv.(config.Hooks)
	}
	return wrapped{"config", cfg}, nil
}
