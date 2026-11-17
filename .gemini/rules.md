# SKIPPER: PROJECT CONSTITUTION & ARCHITECTURAL INVARIANTS

## 1. Project Identity & Goal
- **Name:** Skipper — Unified Container & Kubernetes Runtime Abstraction CLI.
- **Core Mission:** A developer-facing CLI and control plane that abstracts container build, deployment, observability, production debugging, and governance. It bridges local development (Docker-familiar ergonomics) with production Kubernetes through persistent operational memory and deterministic diagnostics.

---

## 2. Unbreakable Engineering Invariants (Zero Drift Rules)

1. **Zero Vendor Lock-In:**
   - Every abstraction compiles down to standard Kubernetes manifests (Deployments, Services, NetworkPolicies, PodDisruptionBudgets) and standard OCI container images.
   - If a user ejects, raw `kubectl`, `helm`, and standard GitOps engines (`argo`, `flux`) must work cleanly with the output. Never create proprietary, non-exportable runtime prisons.

2. **Bidirectional Environment Parity:**
   - Local workflows (`skipper up` with Kind/K3d/Docker) and production workflows (`skipper deploy` with EKS/GKE/AKS/Baremetal) must share identical semantics, networking contracts, and secrets handling.

3. **Persistent Operational Memory (No Stateless Debugging):**
   - Kubernetes runtime pods are ephemeral; Skipper's operational memory is stateful.
   - Always preserve: `ChangeRecord` (who changed what, when, commit SHA), `DebugSessionRecord` (commands executed, pcap/strace, exit codes), and `CausalEvent` (mutation $\to$ symptom correlation).

4. **Deterministic Diagnostics Before Probabilistic AI:**
   - Always evaluate hard deterministic rules first (cgroup OOM signals, exit codes 137/139/1, TCP connection resets, Kubernetes conditions).
   - AI serves as an augmented reasoner and synthesizer over actual operational memory context, never as an ungrounded guesser.
   - Automated remediation in production strictly requires human-in-the-loop approval or policy verification.

5. **Strong Defaults, Principled Escape Hatches:**
   - Developers should write $< 30$ lines of declarative intent (`skipper.yaml`).
   - The platform auto-synthesizes resource limits, probes, security contexts, and network policies.
   - Advanced users must always have clean escape hatches (patches/overlays) to inject custom Kubernetes YAML.

---

## 3. Approved Tech Stack & Tooling Boundaries
- **Language / Runtime:** Go (CLI core, runtime engine, Kubernetes client-go, OCI integration).
- **Embedded / Local Storage:** SQLite (WAL mode) / DuckDB / JSONL persistent ledger under `~/.skipper/opmem`.
- **Production Hub Storage:** PostgreSQL + ClickHouse / Timescale + pgvector.
- **Target Container Engines:** Docker API, Kind, standard Kubernetes API (`client-go`).
- **Specification:** `skipper.yaml` governed by `schemas/skipper.schema.json`.

---

## 4. Execution Roadmap Reference
Refer directly to [README.md](file:///c:/Users/hp/personal/container/README.md) for current phase status and checklist.
- Phase 0: System Architecture & RFC Specifications (Completed)
- Phase 1: Unified CLI Engine & Dual-Target Runtime Adapter (Completed)
- Phase 2: Autonomous Buildpacks & Production Manifest Generation (Completed)
- Phase 3: Persistent Operational Memory & Causal Ledger (Completed)
- Phase 4: Deterministic Diagnostics & Context-Grounded Reasoning Engine (Completed)
- Phase 5: Platform Governance, FinOps Rightsizing & Multi-Tenancy (Completed)
- Phase 6: Production Hardening, Test Verification & Quality Assurance (Completed)
