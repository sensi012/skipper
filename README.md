# Skipper: Unified Container & Kubernetes Engine

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Report Card](https://img.shields.io/badge/go_report-A%2B-brightgreen)](https://goreportcard.com)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows-blue)](docs/architecture.md)
[![Tests](https://img.shields.io/badge/tests-23%2F23%20passing-success)](.github/workflows/ci.yaml)
[![CI](https://img.shields.io/badge/CI-race%20%7C%20lint%20%7C%20vulncheck-brightgreen)](.github/workflows/ci.yaml)

> **Skipper** is a single-binary developer CLI that eliminates container and Kubernetes complexity. Define your app in a simple 20-line file (`skipper.yaml`) — or drop directly into an existing Docker Compose repository — to build, run locally with live reload, deploy progressively, diagnose crashes with deterministic root-cause analysis, and optimize cloud spend.

---

## ⚡ 60-Second Quickstart

### 1. Install Skipper

**Linux & macOS:**
```bash
curl -fsSL https://raw.githubusercontent.com/sensi012/skipper/main/scripts/install.sh | bash
```

**Windows (PowerShell):**
```powershell
iwr -useb https://raw.githubusercontent.com/sensi012/skipper/main/scripts/install.ps1 | iex
```
*(Downloads SHA256-verified binaries for Linux/macOS/Windows via [GitHub Releases](.github/workflows/release.yaml), or builds from source when run inside a cloned repository. Package manifests are also provided for Homebrew [`Formula/skipper.rb`](Formula/skipper.rb), Scoop, and Winget.)*

### 2. Verify Your Environment
```bash
skipper doctor
```

### 3. Bootstrap Any Codebase in Seconds
Navigate to your Go, Node.js, Python, Rust, or Java repository:
```bash
skipper init
```
This generates a production-hardened `skipper.yaml`:
```yaml
version: "v1alpha1"
name: "payment-api"
team: "checkout"
type: "service"

endpoints:
  - name: "http"
    port: 8080
    public: true

runtime:
  replicas:
    min: 1
    max: 3
  resources:
    cpu:
      request: "100m"
      limit: "500m"
    memory:
      request: "128Mi"
      limit: "512Mi"

security:
  runAsNonRoot: true
```

### 4. Start Live Development Loop (Hot-Reload)
```bash
skipper dev
```
Modify any code file—Skipper detects the edit, rebuilds the container, and synchronizes changes in milliseconds.

---

## 🚀 Core Developer Workflow

| Command | Action |
| :--- | :--- |
| `skipper doctor` | Audit local prerequisites (Docker daemon, `kubectl`, `kind`, and port availability). |
| `skipper init` | Auto-detect Go, Node.js, Python, Rust, or Java projects and scaffold `skipper.yaml`. |
| `skipper build` | Auto-detect stack and synthesize smart multi-stage Dockerfiles (Node.js SPA/Vite/CRA -> Nginx & backend/TypeScript -> Distroless; Python `pyproject.toml`, FastAPI/uvicorn & Gunicorn; Go/Rust/Java), plus Docker Compose multi-service and monorepo discovery builds. |
| `skipper up` / `down` / `status` | Launch, inspect, or stop single-service Docker/Kubernetes workloads or multi-service Docker Compose stacks (`--compose`, `--service`, `--build`, `-v/--volumes`) with auto-detection and `--format json`. |
| `skipper logs` | Stream container or pod logs (`skipper logs [name] --follow --tail 100`). |
| `skipper dev` | Foreground watcher that synchronizes edits and hot-reloads the application. |
| `skipper local` | Create, inspect, or tear down a local Kind Kubernetes cluster (`skipper local init\|status\|destroy`). |
| `skipper secret set K=V` | Store local credentials and export standard Kubernetes `v1/Secret` manifests. |
| `skipper secret sync` | Sync secrets with HashiCorp Vault KV v2 (`push`/`pull`) or generate ExternalSecrets manifests (`vault`, `aws`, `gcp`). |
| `skipper port-forward` | Open a local tunnel directly to remote Kubernetes pods (`skipper port-forward svc 8080:8080`). |
| `skipper manifest` | Export hardened, zero-lock-in Kubernetes manifests (`Deployment`, `Service`, `NetworkPolicy`, `PDB`). |
| `skipper scan` | Run shift-left OCI image vulnerability scanning (Trivy, Docker Scout, or static baseline). |
| `skipper sbom` | Generate CycloneDX 1.5 or SPDX 2.3 Software Bills of Materials (`syft`, `docker sbom`, or native parser). |
| `skipper sign` | Sign or verify OCI images with Cosign (key-based or keyless OIDC, with `--dry-run` preview). |
| `skipper policy-check` | Validate `skipper.yaml` against organizational governance and security compliance rules. |
| `skipper deploy` | Deploy workloads with automated Canary/Blue-Green traffic shifting and health gates. |
| `skipper rollback` | Execute or dry-run live Kubernetes deployment rollbacks (`kubectl rollout undo`) and log them to operational memory. |
| `skipper observe` | Query Prometheus/OTel canary health gates (error rate, P95 latency, saturation) or emit OpenTelemetry `ServiceMonitor` manifests. |
| `skipper gitops export` | Export Kustomize overlays plus ArgoCD `Application` or FluxCD `GitRepository` + `Kustomization` sync manifests. |
| `skipper context` | Manage multi-cluster kubeconfig contexts (`list`, `current`, `use`) and detect live-vs-desired cluster drift (`drift`). |
| `skipper diagnose` | Run deterministic root-cause analysis on failing pods (OOMKilled, CrashLoop, probe fails). |
| `skipper debug --ephemeral` | Attach an ephemeral debug sandbox (`kubectl debug` or Docker sidecar) and record the session in operational memory. |
| `skipper time-travel` | Inspect the append-only causal mutation and debug session timeline for a workload. |
| `skipper rightsize` | Compare requested vs live (`--prometheus-url` / `--live`) or baseline P95 compute usage to identify wasted cloud spend. |
| `skipper import compose` | Migrate legacy `docker-compose.yaml` files into native `skipper.yaml`. |
| `skipper mcp` | Launch official Model Context Protocol (MCP) server over stdio for AI agents. |
| `skipper explain` | Output operational mental model (`--format json`) & embedded JSON schema (`--schema`) for AI agent task planning. |
| `skipper --version` | Display the installed Skipper CLI version. |

👉 **Full command syntax & examples:** See the [CLI Reference Manual](docs/cli-reference.md).

---

## 🤖 Built for Developers & AI Agents Alike

Skipper provides a dual-interface design: human developers enjoy rich visual CLI diagnostics and interactive scaffolding, while AI coding agents (Claude Code, Cursor, Windsurf, Antigravity) gain programmatic superpowers:

1. **Global `--format json` Flag:** Every structured CLI command emits strict, machine-parsable JSON on stdout while routing progress logs to stderr (`skipper doctor -f json`, `skipper up -f json`, `skipper status -f json`, `skipper diagnose -f json`, `skipper policy-check -f json`, `skipper explain -f json`).
2. **Machine-Actionable Remediations:** Every diagnostic failure includes a copy-paste or agent-executable `remediationCmd` for automated self-healing.
3. **Official Model Context Protocol (MCP) Server:** Run `skipper mcp` to connect Skipper directly to MCP-compatible AI clients over stdio, exposing all 10 operational tools:
   - `skipper_doctor` — Audit local runtime prerequisites and ports
   - `skipper_init` — Inspect project stack and scaffold `skipper.yaml`
   - `skipper_policy_check` — Evaluate organizational security and compliance policies
   - `skipper_manifest` — Synthesize hardened Kubernetes YAML bundles
   - `skipper_diagnose` — Run the 8-detector deterministic crash root-cause matrix
   - `skipper_rightsize` — Analyze live (Prometheus / `kubectl top`) or baseline P95 utilization for cost savings
   - `skipper_rollback` — Execute or dry-run `kubectl rollout undo` and record causal ledger events
   - `skipper_investigate_incident` — Correlate operational memory timeline events with deterministic log diagnostics
   - `skipper_sbom` — Generate CycloneDX 1.5 or SPDX 2.3 SBOM reports for source dirs or OCI images
   - `skipper_drift_check` — Compute field-level drift between desired and live Kubernetes manifests
4. **Self-Introspection & Embedded JSON Schema:** `skipper explain` (and `skipper explain --format json`) outputs the structured operational specification and playbook, and `skipper explain --schema` serves the embedded JSON Schema Draft 2020-12 specification of `skipper.yaml` from any working directory.

---

## 🎯 Why Skipper?

```
    Developer Intent (skipper.yaml / compose.yaml)
                          │
                          ▼
                 ┌─────────────────┐
                 │   SKIPPER CLI   │
                 └────────┬────────┘
                          │
             ┌────────────┴────────────┐
             ▼                         ▼
   Local Dev (Docker/Compose/Kind)  Production Kubernetes
   - 60s hot-reload                 - Hardened manifests (seccomp, non-root)
   - Multi-service Compose support  - Zero-trust NetworkPolicies & PDBs
   - Smart Node/Python/Go synthesis - Deterministic crash diagnostics
                                    - FinOps rightsizing & GitOps export
```

1. **Zero Vendor Lock-In:** Every abstraction compiles into clean, standard Kubernetes YAML and standard OCI images. You can eject at any moment—vanilla `kubectl`, `helm`, and ArgoCD/Flux run the output without custom daemons.
2. **No Dockerfile Boilerplate:** Automatically synthesizes minimal, secure multi-stage images (Nginx for Node SPAs, Distroless non-root runtimes for Go/Node/Python/Rust/Java backends) without cluttering your repository.
3. **Deterministic Diagnostics Before AI:** If a container crashes, Skipper checks hard kernel signals (exit codes 137/139/1, cgroup OOM pressure, probe timeouts) to deliver instant root-cause analysis.
4. **Persistent Operational Memory:** Kubernetes pods are ephemeral; Skipper’s ledger remembers every change, mutation, and debug session leading up to an incident (`skipper time-travel`).

---

## 📊 Quality, CI & Performance Benchmarks

Every pull request and push to `main`/`master` is gated by our automated Linux GitHub Actions CI workflow ([`.github/workflows/ci.yaml`](.github/workflows/ci.yaml)), which enforces `gofmt`, `go vet`, the Go race detector (`go test -race -count=1 -coverprofile=coverage.out ./...`), `golangci-lint`, and `govulncheck` across all 23 packages.

Executed on standard developer hardware (`windows/amd64`, Intel Core i5 @ 2.40GHz):

| Operation | Latency | Memory / Op | Allocations |
| :--- | :--- | :--- | :--- |
| **Policy Evaluation** (`skipper policy-check`) | **14.2 ns** | 0 B | 0 allocs |
| **Manifest Synthesis** (`skipper manifest`) | **22.4 µs** | 10.9 KB | 70 allocs |
| **Language Detection** (`skipper build`) | **142.3 µs** | 912 B | 9 allocs |
| **Deterministic Matrix** (`skipper diagnose`) | **902.4 µs** | 1.8 KB | 16 allocs |
| **Causal Ledger Write** (`skipper time-travel`) | **821.1 µs** | 921 B | 7 allocs |

---

## 📚 Documentation & Architecture

* **[CLI Reference Manual](docs/cli-reference.md):** Complete guide to all 28 Skipper CLI commands, subcommands, flags, and 10 MCP tools.
* **[Real-World Integration Harness](docs/realworld-testing.md):** Repeatable end-to-end validation suite across public repos and failure fixtures.
* **[System Architecture & RFCs](docs/architecture.md):** Architectural constitution, invariant rules, and phased deliverables.
* **[RFC 001: Intent Specification](docs/rfcs/001-skipper-intent-specification.md):** Specification syntax and Docker Compose mapping.
* **[RFC 002: Operational Memory](docs/rfcs/002-operational-memory-models.md):** Causal event graphs and timeline models.
* **[RFC 003: Protocol Architecture](docs/rfcs/003-storage-protocol-architecture.md):** Local SQLite vs fleet-level storage.
* **[Contributing Guidelines](CONTRIBUTING.md):** Setup, test execution, and pull request conventions.

---

## ⚖️ License

Distributed under the [Apache 2.0 License](LICENSE).
