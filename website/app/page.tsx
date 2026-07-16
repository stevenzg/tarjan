import type { ReactNode } from "react";

const GITHUB = "https://github.com/stevenzg/tarjan";

export default function Home() {
  return (
    <div className="relative overflow-hidden">
      <BackdropGlow />
      <Nav />
      <Hero />
      <Problem />
      <Features />
      <ConfigShowcase />
      <Commands />
      <CallToAction />
      <Footer />
    </div>
  );
}

function BackdropGlow() {
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute inset-x-0 top-0 -z-10 h-[600px] bg-[radial-gradient(60%_60%_at_50%_0%,rgba(99,102,241,0.18),transparent_70%)]"
    />
  );
}

function Nav() {
  return (
    <header className="mx-auto flex max-w-6xl items-center justify-between px-6 py-5">
      <div className="flex items-center gap-2 font-mono text-lg font-bold tracking-tight">
        <span className="text-indigo-400">›</span> tarjan
      </div>
      <nav className="flex items-center gap-6 text-sm text-white/70">
        <a href="#features" className="hover:text-white">
          Features
        </a>
        <a href="/docs" className="hover:text-white">
          Docs
        </a>
        <a
          href={GITHUB}
          className="rounded-lg border border-white/15 px-3 py-1.5 font-medium text-white hover:bg-white/5"
        >
          GitHub ↗
        </a>
      </nav>
    </header>
  );
}

function Hero() {
  return (
    <section className="mx-auto max-w-6xl px-6 pt-16 pb-12 text-center sm:pt-24">
      <span className="inline-flex items-center gap-2 rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-white/70">
        <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
        Terraform / Aspire — but for your{" "}
        <em className="not-italic text-white">local</em> dev environment
      </span>
      <h1 className="mx-auto mt-6 max-w-4xl text-balance text-4xl font-bold leading-tight tracking-tight sm:text-6xl">
        Spin up a whole product&apos;s local dev environment in{" "}
        <span className="bg-gradient-to-r from-indigo-400 to-emerald-300 bg-clip-text text-transparent">
          one command
        </span>
      </h1>
      <p className="mx-auto mt-6 max-w-2xl text-pretty text-lg text-white/65">
        Clone five repos, start Postgres, install deps, run the API, boot the web
        app and the mobile app, wire up the cloud bits. tarjan turns that checklist
        into{" "}
        <code className="rounded bg-white/10 px-1.5 py-0.5 font-mono text-sm">
          tarjan up
        </code>
        .
      </p>

      <div className="mx-auto mt-8 flex max-w-md items-center justify-between rounded-xl border border-white/10 bg-black/40 px-4 py-3 font-mono text-sm">
        <span>
          <span className="text-white/40">$</span> tarjan up
        </span>
        <span className="text-xs text-white/40">single static binary</span>
      </div>

      <div className="mt-8 flex items-center justify-center gap-3">
        <a
          href="/docs"
          className="rounded-lg bg-white px-5 py-2.5 text-sm font-semibold text-black hover:bg-white/90"
        >
          Get started
        </a>
        <a
          href={GITHUB}
          className="rounded-lg border border-white/15 px-5 py-2.5 text-sm font-semibold hover:bg-white/5"
        >
          Star on GitHub
        </a>
      </div>
    </section>
  );
}

function Problem() {
  return (
    <section className="mx-auto max-w-5xl px-6 py-12">
      <div className="grid gap-6 rounded-2xl border border-white/10 bg-white/[0.02] p-8 md:grid-cols-2">
        <div>
          <p className="text-sm font-semibold uppercase tracking-wider text-rose-300/80">
            Without tarjan
          </p>
          <ul className="mt-4 space-y-2 text-sm text-white/60">
            <li>· clone repo A, B, C, D…</li>
            <li>· install .NET / Node / Docker by hand</li>
            <li>· start the database, run migrations</li>
            <li>· start each backend in the right order</li>
            <li>· npm install, start the frontend, the app…</li>
            <li>· add every folder to your editor</li>
          </ul>
        </div>
        <div>
          <p className="text-sm font-semibold uppercase tracking-wider text-emerald-300/80">
            With tarjan
          </p>
          <pre className="mt-4 overflow-x-auto rounded-lg bg-black/50 p-4 font-mono text-sm text-white/80">
            {`$ tarjan up
✓ tools checked
✓ repos cloned → fresh workspace
✓ VS Code workspace generated
✓ postgres · migrate · api · web ready
  environment is up — Ctrl+C to stop`}
          </pre>
        </div>
      </div>
    </section>
  );
}

const FEATURES: { title: string; body: string }[] = [
  {
    title: "Concurrent, dependency-ordered",
    body: "Services start in parallel, each gated on its dependencies' health checks (TCP / HTTP / command). Cycles are rejected up front.",
  },
  {
    title: "Self-healing",
    body: "Crash isolation with per-service restart policies and backoff. One service dying never takes the environment down.",
  },
  {
    title: "Live reload & hot restart",
    body: "Edit the config → tarjan reload reconciles in place. Watch paths to restart a service on file change.",
  },
  {
    title: "Local + cloud, together",
    body: "Declare external cloud dependencies (probed, not started), tunnels as services, and pre-up hooks for auth and secrets.",
  },
  {
    title: "Tool bootstrap",
    body: "tarjan doctor --install installs the missing toolchain per-OS — opt-in, never behind your back.",
  },
  {
    title: "Single binary",
    body: "Written in Go: a single static binary for macOS, Linux and Windows. No runtime to install just to run the launcher.",
  },
  {
    title: "Jobs & pipelines",
    body: "kind: job runs to completion; dependents wait for exit 0. Chain them for migrations and ETL/ML steps.",
  },
  {
    title: "Config in code",
    body: "YAML by default, or Starlark (tarjan.star) when you want loops, conditionals and computed values — same single binary.",
  },
];

function Features() {
  return (
    <section id="features" className="mx-auto max-w-6xl px-6 py-16">
      <h2 className="text-center text-3xl font-bold tracking-tight">
        Everything a real product needs to run locally
      </h2>
      <div className="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {FEATURES.map((f) => (
          <Card key={f.title} title={f.title}>
            {f.body}
          </Card>
        ))}
      </div>
    </section>
  );
}

function Card({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="rounded-xl border border-white/10 bg-white/[0.02] p-5 transition hover:border-white/20 hover:bg-white/[0.04]">
      <h3 className="font-semibold">{title}</h3>
      <p className="mt-2 text-sm leading-relaxed text-white/60">{children}</p>
    </div>
  );
}

function ConfigShowcase() {
  return (
    <section className="mx-auto max-w-5xl px-6 py-12">
      <h2 className="text-center text-3xl font-bold tracking-tight">
        One declarative file
      </h2>
      <p className="mx-auto mt-3 max-w-2xl text-center text-white/60">
        Repos to clone, tools to require, services to run — with dependencies,
        health checks and restart policies.
      </p>
      <pre className="mx-auto mt-8 max-w-3xl overflow-x-auto rounded-2xl border border-white/10 bg-black/50 p-6 font-mono text-[13px] leading-relaxed text-white/80">
        {`name: acme
repos:
  - { name: api, url: https://github.com/acme/api.git }
  - { name: web, url: https://github.com/acme/web.git }

services:
  - name: postgres
    docker: { image: postgres:16, ports: ["5432:5432"] }
    health: { tcp: "localhost:5432" }

  - name: migrate          # run-to-completion job
    kind: job
    command: "dotnet ef database update"
    dependsOn: [postgres]

  - name: api
    workdir: api
    command: "dotnet run"
    dependsOn: [migrate]
    health: { http: "http://localhost:5080/health" }
    restart: on-failure
    watch: { paths: ["src"] }

  - name: web
    workdir: web
    command: "npm run dev"
    dependsOn: [api]

workspace: { vscode: true }`}
      </pre>
    </section>
  );
}

const COMMANDS: [string, string][] = [
  ["tarjan up", "clone, check tools, generate workspace, start everything"],
  ["tarjan status --watch", "live readiness of every service"],
  ["tarjan ui", "full-screen dashboard with logs + restart/reload"],
  ["tarjan restart api", "restart one service in place"],
  ["tarjan reload", "reconcile a running env to the edited config"],
  ["tarjan exec api -- npm test", "run in a service's dir + environment"],
  ["tarjan logs api -f", "follow a service's captured logs"],
  ["tarjan doctor --install", "install the missing toolchain"],
];

function Commands() {
  return (
    <section className="mx-auto max-w-4xl px-6 py-16">
      <h2 className="text-center text-3xl font-bold tracking-tight">
        A command for every step of the loop
      </h2>
      <div className="mt-8 divide-y divide-white/5 overflow-hidden rounded-2xl border border-white/10">
        {COMMANDS.map(([cmd, desc]) => (
          <div
            key={cmd}
            className="flex flex-col gap-1 px-5 py-3.5 sm:flex-row sm:items-center sm:justify-between"
          >
            <code className="font-mono text-sm text-emerald-300">{cmd}</code>
            <span className="text-sm text-white/55">{desc}</span>
          </div>
        ))}
      </div>
    </section>
  );
}

function CallToAction() {
  return (
    <section className="mx-auto max-w-3xl px-6 py-16 text-center">
      <h2 className="text-3xl font-bold tracking-tight">
        Bring the whole thing up.
      </h2>
      <p className="mx-auto mt-3 max-w-xl text-white/60">
        Download a prebuilt binary, point a config at your repos, and run{" "}
        <code className="rounded bg-white/10 px-1.5 py-0.5 font-mono text-sm">
          tarjan up
        </code>
        .
      </p>
      <div className="mt-7 flex items-center justify-center gap-3">
        <a
          href="/docs"
          className="rounded-lg bg-white px-5 py-2.5 text-sm font-semibold text-black hover:bg-white/90"
        >
          Read the docs
        </a>
        <a
          href={`${GITHUB}/releases/latest`}
          className="rounded-lg border border-white/15 px-5 py-2.5 text-sm font-semibold hover:bg-white/5"
        >
          Download
        </a>
      </div>
    </section>
  );
}

function Footer() {
  return (
    <footer className="border-t border-white/10">
      <div className="mx-auto flex max-w-6xl flex-col items-center justify-between gap-3 px-6 py-8 text-sm text-white/45 sm:flex-row">
        <span className="font-mono">› tarjan</span>
        <span>MIT licensed · built with Go</span>
        <a href={GITHUB} className="hover:text-white/70">
          github.com/stevenzg/tarjan
        </a>
      </div>
    </footer>
  );
}
