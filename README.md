# Skipper

[![CI](https://github.com/sensi012/skipper/actions/workflows/ci.yaml/badge.svg)](https://github.com/sensi012/skipper/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/sensi012/skipper)](https://github.com/sensi012/skipper/releases)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows-blue)](docs/architecture.md)

**Skipper** is a single-binary CLI that unifies building, running, deploying, and diagnosing apps across local Docker, Docker Compose, and Kubernetes. Start from a short `skipper.yaml` or drop straight into an existing `docker-compose.yml` with zero boilerplate.

---

## Install

**Linux & macOS:**
```bash
curl -fsSL https://raw.githubusercontent.com/sensi012/skipper/main/scripts/install.sh | bash
```

**Windows (PowerShell):**
```powershell
iwr -useb https://raw.githubusercontent.com/sensi012/skipper/main/scripts/install.ps1 | iex
```

Both installers verify SHA-256 checksums against [GitHub Releases](https://github.com/sensi012/skipper/releases) (and build from source if run inside a cloned repo). Package manifests are also included for Homebrew ([`Formula/skipper.rb`](Formula/skipper.rb)), Scoop, and Winget.

---

## 60-Second Quickstart

### Single-Service App
```bash
skipper doctor   # Verify Docker, kubectl, kind, and ports
skipper init     # Detect stack and generate skipper.yaml
skipper build    # Build OCI image (no Dockerfile required for Go, Node.js, or Python)
skipper up       # Run locally on Docker (or add -r kubernetes)
```

For active coding, run `skipper dev` to watch files, rebuild, and redeploy on save.

### Existing Docker Compose Repo (Zero Config)
In any repository with `compose.yaml` or `docker-compose.yml` (and no `skipper.yaml`), Skipper auto-detects Compose mode:

```bash
skipper build
skipper up
skipper status
skipper down -v
```

---

## Core Commands

| Workflow | Commands | Details |
| :--- | :--- | :--- |
| **Environment & Setup** | `skipper doctor`, `skipper init`, `skipper dev` | Check prerequisites, scaffold `skipper.yaml`, live reload |
| **Build & Run** | `skipper build`, `skipper up`, `skipper status`, `skipper logs`, `skipper down` | Local Docker, multi-service Compose, or Kubernetes workloads |
| **Local Kubernetes** | `skipper local init\|status\|destroy` | Spin up a local `kind` cluster |
| **Deploy & GitOps** | `skipper manifest`, `skipper deploy`, `skipper rollback`, `skipper gitops export`, `skipper context` | Canary/Blue-Green rollouts, live rollbacks, drift checks, ArgoCD/Flux |
| **Security & SBOM** | `skipper scan`, `skipper sbom`, `skipper sign`, `skipper policy-check`, `skipper secret` | Trivy/Scout scans, CycloneDX/SPDX SBOMs, Cosign, Vault/AWS/GCP sync |
| **Diagnostics & FinOps** | `skipper diagnose`, `skipper debug --ephemeral`, `skipper time-travel`, `skipper rightsize`, `skipper observe` | Crash root-cause matrix, debug sandboxes, audit ledger, P95 sizing |
| **AI & Automation** | `skipper mcp`, `skipper explain`, `--format json` | 10-tool stdio MCP server and strict JSON output |

See the full **[CLI Reference Manual](docs/cli-reference.md)** for all 28 commands, flags, and examples.

---

## Smart Builds without Dockerfile Boilerplate

When a directory has no `Dockerfile`, `skipper build` auto-detects your stack:

- **Node.js (`package.json`)**: SPAs (Vite, CRA, Angular, Vue, Svelte, Parcel, Gatsby) synthesize 2-stage `node:20-alpine` → `nginx:alpine` images; backends & TypeScript apps target `gcr.io/distroless/nodejs20-debian12:nonroot`.
- **Python (`requirements.txt` or `pyproject.toml`)**: Auto-detects Python version from `.python-version` / `requires-python` (default `3.12`) and configures FastAPI/`uvicorn`, Gunicorn/Django, or script entrypoints cleanly.
- **Go (`go.mod`)**: Compiles a static binary into `gcr.io/distroless/static-debian12:nonroot`.
- **Rust, Java & Custom Stacks**: Auto-detected and built using your existing `Dockerfile` or Cloud Native Buildpacks (`pack`).

---

## Built for Developers & AI Agents

- **Global `--format json`**: Emits clean, machine-parsable JSON on `stdout` while routing progress banners to `stderr`.
- **Actionable Remediations**: Diagnostic and policy failures return a structured `remediationCmd`.
- **Official MCP Server & Introspection**: `skipper mcp` exposes 10 operational tools over stdio, and `skipper explain --schema` outputs the embedded `skipper.yaml` JSON Schema. See [CLI Reference Manual](docs/cli-reference.md#ai-agent-integration--mcp-server).

---

## Testing & Quality

Every push to `main` runs automated Linux CI ([`.github/workflows/ci.yaml`](.github/workflows/ci.yaml): `gofmt`, `go vet`, `go test -race` across all 23 packages, `golangci-lint`, and `govulncheck`), complemented by a real-world integration suite covering public Docker/Compose repos, clean installs, live `kind` Kubernetes rollbacks, and failure ergonomics ([Real-World Testing Guide](docs/realworld-testing.md)).

---

## Documentation

- **[CLI Reference Manual](docs/cli-reference.md)** — All 28 commands, flags, JSON contracts, and 10 MCP tools
- **[Real-World Integration Harness](docs/realworld-testing.md)** — End-to-end validation across public repos, `kind`, and failure modes
- **[System Architecture & Roadmap](docs/architecture.md)** — Design invariants, package layout, and implementation status
- **[RFCs](docs/rfcs/001-skipper-intent-specification.md)** — [001 Intent Spec](docs/rfcs/001-skipper-intent-specification.md) · [002 Operational Memory](docs/rfcs/002-operational-memory-models.md) · [003 Storage Protocol](docs/rfcs/003-storage-protocol-architecture.md)
- **[Contributing Guidelines](CONTRIBUTING.md)** — Setup, pre-PR checks, and conventions

## License

Distributed under the [Apache 2.0 License](LICENSE).
