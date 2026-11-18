# Skipper: Unified Container & Kubernetes Engine

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Report Card](https://img.shields.io/badge/go_report-A%2B-brightgreen)](https://goreportcard.com)
[![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20macOS%20%7C%20Windows-blue)](docs/architecture.md)
[![Tests](https://img.shields.io/badge/tests-16%2F16%20passing-success)](docs/architecture.md)

> **Skipper** is a single-binary developer CLI that eliminates container and Kubernetes complexity. Define your app in a simple 20-line file (`skipper.yaml`) to build, run locally with live reload, deploy progressively, diagnose crashes with deterministic root-cause analysis, and optimize cloud spend.

---

## ⚡ 60-Second Quickstart

### 1. Install Skipper

**Linux & macOS:**
```bash
curl -fsSL https://raw.githubusercontent.com/unified-platform/container/master/scripts/install.sh | bash
```

**Windows (PowerShell):**
```powershell
iwr -useb https://raw.githubusercontent.com/unified-platform/container/master/scripts/install.ps1 | iex
```
*(Or download the pre-built static binaries directly from [`bin/`](bin/)).*

### 2. Verify Your Environment
```bash
skipper doctor
```

### 3. Bootstrap Any Codebase in Seconds
Navigate to your Go, Node.js, Python, or Rust repository:
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
| `skipper build` | Auto-detect language and synthesize an optimized multi-stage distroless OCI image. |
| `skipper up` / `down` | Launch or terminate the workload in Docker or a local Kind cluster. |
| `skipper dev` | Foreground watcher that synchronizes edits and hot-reloads the application. |
| `skipper secret set K=V` | Store local credentials and export standard Kubernetes `v1/Secret` manifests. |
| `skipper port-forward` | Open a local tunnel directly to remote Kubernetes pods (`skipper port-forward svc 8080:8080`). |
| `skipper manifest` | Export hardened, zero-lock-in Kubernetes manifests (`Deployment`, `Service`, `NetworkPolicy`, `PDB`). |
| `skipper deploy` | Deploy workloads with automated Canary/Blue-Green traffic shifting and health gates. |
| `skipper diagnose` | Run deterministic root-cause analysis on failing pods (OOMKilled, CrashLoop, probe fails). |
| `skipper rightsize` | Compare requested vs P95 compute usage to identify wasted cloud spend. |
| `skipper import compose` | Migrate legacy `docker-compose.yaml` files into native `skipper.yaml`. |

👉 **Full command syntax & examples:** See the [CLI Reference Manual](docs/cli-reference.md).

---

## 🎯 Why Skipper?

```
    Developer Intent (skipper.yaml)
                │
                ▼
       ┌─────────────────┐
       │   SKIPPER CLI   │
       └────────┬────────┘
                │
   ┌────────────┴────────────┐
   ▼                         ▼
Local Dev (Docker/Kind)   Production Kubernetes
- 60s hot-reload         - Hardened manifests (seccomp, non-root)
- Instant port forwarding - Zero-trust NetworkPolicies & PDBs
- Zero Dockerfile hassle - Deterministic crash diagnostics
                         - FinOps rightsizing & cost analysis
```

1. **Zero Vendor Lock-In:** Every abstraction compiles into clean, standard Kubernetes YAML and standard OCI images. You can eject at any moment—vanilla `kubectl`, `helm`, and ArgoCD/Flux run the output without custom daemons.
2. **No Dockerfile Boilerplate:** Automatically builds minimal, secure multi-stage images (using Distroless non-root runtimes) without cluttering your repository.
3. **Deterministic Diagnostics Before AI:** If a container crashes, Skipper checks hard kernel signals (exit codes 137/139/1, cgroup OOM pressure, probe timeouts) to deliver instant root-cause analysis.
4. **Persistent Operational Memory:** Kubernetes pods are ephemeral; Skipper’s ledger remembers every change, mutation, and debug session leading up to an incident (`skipper time-travel`).

---

## 📊 Performance Benchmarks

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

* **[CLI Reference Manual](docs/cli-reference.md):** Complete guide to all 16 commands and subcommands.
* **[System Architecture & RFCs](docs/architecture.md):** Architectural constitution, invariant rules, and phased deliverables.
* **[RFC 001: Intent Specification](docs/rfcs/001-skipper-intent-specification.md):** Specification syntax and Docker Compose mapping.
* **[RFC 002: Operational Memory](docs/rfcs/002-operational-memory-models.md):** Causal event graphs and timeline models.
* **[RFC 003: Protocol Architecture](docs/rfcs/003-storage-protocol-architecture.md):** Local SQLite vs fleet-level storage.
* **[Contributing Guidelines](CONTRIBUTING.md):** Setup, test execution, and pull request conventions.

---

## ⚖️ License

Distributed under the [Apache 2.0 License](LICENSE).
