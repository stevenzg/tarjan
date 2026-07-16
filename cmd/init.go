package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/stevenzg/tarjan/internal/ui"
)

var (
	initForce bool
	initStar  bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Write a starter tarjan.yaml (or tarjan.star with --star)",
	RunE:  runInit,
}

func init() {
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "overwrite an existing config")
	initCmd.Flags().BoolVar(&initStar, "star", false, "scaffold a Starlark tarjan.star instead of YAML")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	path, content := "tarjan.yaml", starterConfig
	if initStar {
		path, content = "tarjan.star", starterStar
	}
	if _, err := os.Stat(path); err == nil && !initForce {
		return fmt.Errorf("%s already exists (use --force to overwrite)", path)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	ui.Success("wrote %s", path)
	ui.Info("edit it, then run `tarjan up`")
	return nil
}

const starterStar = `# tarjan.star — a tarjan config in Starlark (a small, sandboxed Python-like language).
# Use it instead of tarjan.yaml when you want loops, conditionals or computed
# values. It produces the same config; assign the result to a top-level "tarjan".

services = [
    service(
        name = "postgres",
        docker = docker(image = "postgres:16", ports = ["5432:5432"], env = {"POSTGRES_PASSWORD": "dev"}),
        health = health(tcp = "localhost:5432"),
    ),
]

# Generate several similar backend services from one template.
for i, name in enumerate(["api", "worker"]):
    services.append(service(
        name = name,
        workdir = name,
        setup = ["npm install"],
        command = "npm run dev",
        env = {"PORT": str(8080 + i), "DATABASE_URL": "postgres://postgres:dev@localhost:5432/postgres"},
        depends_on = ["postgres"],
        restart = "on-failure",
        watch = watch(paths = ["src"]),
    ))

tarjan = config(
    name = "myproduct",
    workspace_root = "~/tarjan/myproduct",
    requires = [
        tool(name = "git"),
        tool(name = "node", min_version = "20", install = {"darwin": "brew install node"}),
    ],
    repos = [
        repo(name = "api", url = "https://github.com/your-org/api.git", branch = "main"),
        repo(name = "worker", url = "https://github.com/your-org/worker.git"),
    ],
    services = services,
    workspace = workspace(vscode = True),
)
`

const starterConfig = `# tarjan.yaml — declares a product's local development environment.
name: myproduct

# Each "tarjan up" materialises a fresh, timestamped workspace under here.
workspaceRoot: ~/tarjan/myproduct

# Tools that must exist before the environment starts. With 'tarjan doctor
# --install' (or 'tarjan up --install') a missing tool is installed. Declare
# *what* you need, not *how per OS*: 'mise' installs versioned runtimes via the
# mise version manager; 'package' installs OS clients via the host's package
# manager. 'install' is a per-OS escape hatch. Precedence: install > mise > package.
requires:
  - name: git
  - name: docker
    optional: true
  - name: node
    minVersion: "20"
    mise: node@20                  # versioned runtime → mise
    installHint: "https://nodejs.org"
  - name: psql
    package:                       # OS client → auto-detected package manager
      apt: postgresql-client
      brew: libpq
    installHint: "PostgreSQL client"

# Repositories cloned into the workspace.
repos:
  - name: api
    url: https://github.com/your-org/api.git
    branch: main
  - name: web
    url: https://github.com/your-org/web.git
    branch: main

# Services started in dependency order, each gated on a health check.
services:
  - name: postgres
    docker:
      image: postgres:16
      ports: ["5432:5432"]
      env:
        POSTGRES_PASSWORD: dev
    health:
      tcp: "localhost:5432"

  - name: api
    workdir: api
    setup:
      - "npm install"
    command: "npm run dev"
    env:
      DATABASE_URL: "postgres://postgres:dev@localhost:5432/postgres"
    dependsOn: [postgres]
    health:
      http: "http://localhost:8080/health"
    restart: on-failure   # auto-restart on crash (no | on-failure | always)
    watch:
      paths: ["src"]      # live-restart when these files change

  - name: web
    workdir: web
    setup:
      - "npm install"
    command: "npm run dev"
    dependsOn: [api]
    restart: on-failure

# Generate "<name>.code-workspace" so every repo opens in one VS Code window.
workspace:
  vscode: true
`
