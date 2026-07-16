# The same shape as microservices.yaml, but in Starlark — where N near-identical
# services collapse into a loop instead of N copy-pasted blocks. Run with tarjan
# automatically when the file is named tarjan.star, or `tarjan up -c microservices.star`.

# Shared infrastructure.
services = [
    service(
        name = "postgres",
        docker = docker(image = "postgres:16", ports = ["5432:5432"], env = {"POSTGRES_PASSWORD": "dev", "POSTGRES_DB": "shop"}),
        health = health(tcp = "localhost:5432"),
    ),
    service(
        name = "redis",
        docker = docker(image = "redis:7", ports = ["6379:6379"]),
        health = health(tcp = "localhost:6379"),
    ),
]

# Define the backend services once as data, then generate them. Adding a service
# is one line here instead of a whole YAML block.
BACKENDS = [
    {"name": "catalog", "port": 8081, "deps": ["postgres"]},
    {"name": "orders", "port": 8082, "deps": ["postgres", "redis"]},
    {"name": "gateway", "port": 8080, "deps": ["catalog", "orders"]},
]

repos = []
for b in BACKENDS:
    repos.append(repo(name = b["name"], url = "https://github.com/your-org/" + b["name"] + ".git"))
    services.append(service(
        name = b["name"],
        workdir = b["name"],
        setup = ["go mod download"],
        command = "go run ./cmd/server",
        env = {"PORT": str(b["port"]), "DATABASE_URL": "postgres://postgres:dev@localhost:5432/shop"},
        depends_on = b["deps"],
        health = health(http = "http://localhost:" + str(b["port"]) + "/healthz"),
        restart = "on-failure",
    ))

tarjan = config(
    name = "shop-backend",
    workspace_root = "~/tarjan/shop-backend",
    requires = [tool(name = "git"), tool(name = "docker"), tool(name = "go", min_version = "1.22", optional = True)],
    repos = repos,
    services = services,
    workspace = workspace(vscode = True),
)
