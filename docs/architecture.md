# Skipper System Architecture & Verification Roadmap

---

## 1. Architectural Principles & Invariants (20-Year Engineering Stance)

1. **Convention over Configuration with Zero-Lock-In Escapes:**
   - Every abstraction compiles down to clean, standard Kubernetes manifests (`apps/v1` Deployments, `v1` Services, `networking.k8s.io/v1` NetworkPolicies, `policy/v1` PodDisruptionBudgets) and standard OCI container images.
   - Standard `kubectl`, `helm`, and GitOps controllers (`flux`, `argocd`) execute the output without custom daemon dependencies.

2. **Bidirectional Environment Parity:**
   - Local ephemeral targets (Kind / K3d / Docker) behave with identical semantics, networking contracts, and security contexts to remote production clusters (EKS / GKE / AKS / Baremetal).

3. **Stateless Runtime, Stateful Operational Memory:**
   - Kubernetes controls ephemeral infrastructure; Skipper maintains an immutable temporal ledger (`ChangeRecord`, `DebugSessionRecord`, and `CausalEvent`) tracking change lineage, build provenance, and incident triage history under `~/.skipper/opmem`.

4. **Deterministic Gatekeepers Before Probabilistic AI:**
   - Diagnostic workflows prioritize hard deterministic rules (cgroup OOM signals, exit codes 137/139/1, TCP connection resets, Kubernetes conditions) before invoking LLM reasoning engines.
   - Automated remediation strictly requires policy-controlled verification gates and human approvals in production.

5. **Strong Defaults, Principled Escape Hatches:**
   - Developers write $< 30$ lines of declarative intent (`skipper.yaml`).
   - The platform auto-synthesizes resource boundaries, probes, security contexts, and isolation policies, while providing clean override patches.

---

## 2. Implementation & Roadmap Verification Matrix

### Phase 0: System Architecture & RFC Specifications
- [x] **Step 0.1: Define Application Manifest Specification (`AppSpec` / `skipper.yaml`)**
  - Designed declarative intent specification ([RFC 001](rfcs/001-skipper-intent-specification.md)).
  - Published JSON Schema ([`skipper.schema.json`](../schemas/skipper.schema.json)).
  - Established bi-directional Docker Compose mapping.
- [x] **Step 0.2: Define Core Domain Data Models**
  - Formulated schemas ([RFC 002](rfcs/002-operational-memory-models.md)) for `ChangeRecord`, `DebugSession`, and `CausalGraph`.
- [x] **Step 0.3: Protocol & Storage Selection**
  - Defined dual-mode architecture ([RFC 003](rfcs/003-storage-protocol-architecture.md)): SQLite for local offline development; PostgreSQL + ClickHouse for multi-cluster production fleet control.

### Phase 1: Unified CLI Engine & Dual-Target Runtime Adapter
- [x] **Step 1.1: Initialize Modular Go CLI Architecture**
  - Built unified Cobra CLI framework ([`cmd/skipper/main.go`](../cmd/skipper/main.go)) with standard command vocabulary.
  - Implemented declarative config parser ([`pkg/config/spec.go`](../pkg/config/spec.go)).
  - Compiled and verified single binary `bin/skipper.exe`.
- [x] **Step 1.2: Dual-Target Runtime Abstraction Layer**
  - Implemented `RuntimeEngine` interface ([`pkg/runtime/interface.go`](../pkg/runtime/interface.go)).
  - Built `DockerAdapter` ([`pkg/runtime/docker.go`](../pkg/runtime/docker.go)) and `KubernetesAdapter` ([`pkg/runtime/kubernetes.go`](../pkg/runtime/kubernetes.go)).
- [x] **Step 1.3: Local Development Engine Integration (Kind)**
  - Implemented ephemeral Kind cluster manager ([`pkg/local/cluster.go`](../pkg/local/cluster.go)).

### Phase 2: Autonomous Buildpacks & Production Manifest Synthesizer
- [x] **Step 2.1: Multi-Language Autonomous Builder Engine**
  - Implemented `CNBBuilder` ([`pkg/build/builder.go`](../pkg/build/builder.go)) supporting Go, Node.js, Python, Rust, Java, and Dockerfiles.
- [x] **Step 2.2: Hardened Manifest Synthesizer**
  - Implemented synthesizer ([`pkg/synthesizer/synthesizer.go`](../pkg/synthesizer/synthesizer.go)) generating Deployments, Services, NetworkPolicies, and PDBs.
- [x] **Step 2.3: Shift-Left Vulnerability Scanner**
  - Built `TrivyScanner` ([`pkg/security/scanner.go`](../pkg/security/scanner.go)) with Trivy, Docker Scout, and internal static fallbacks.
- [x] **Step 2.4: Progressive Delivery Controller**
  - Implemented Canary and Blue-Green rollouts with health gates ([`pkg/rollout/controller.go`](../pkg/rollout/controller.go)).

### Phase 3: Persistent Operational Memory & Causal Ledger
- [x] **Step 3.1: Temporal Causal Event Ledger**
  - Built append-only JSONL event storage engine ([`pkg/opmem/ledger.go`](../pkg/opmem/ledger.go)).
- [x] **Step 3.2: Context-Retaining Interactive Debugger**
  - Integrated debug session preservation and ephemeral container launch.

### Phase 4: Deterministic Diagnostics & Root-Cause Matrix
- [x] **Step 4.1: Deterministic Diagnostic Rule Matrix**
  - Implemented 8 detectors ([`pkg/diagnostics/engine.go`](../pkg/diagnostics/engine.go)): OOMKilled, CrashLoopBackOff, ImagePullBackOff, ReadinessProbe failure, PendingPod starvation, NetworkPolicy drop, IngressRouting failure, DNS failure.
- [x] **Step 4.2: Context-Grounded Reasoning Engine**
  - Correlated real-time logs with operational ledger mutation history.

### Phase 5: Platform Governance, FinOps Rightsizing & Multi-Tenancy
- [x] **Step 5.1: Policy-as-Code Compliance Engine**
  - Implemented pre-deployment validation rules ([`pkg/governance/engine.go`](../pkg/governance/engine.go)).
- [x] **Step 5.2: FinOps Workload Rightsizing Analyzer**
  - Implemented resource delta and monthly cloud cost savings analyzer.

### Phase 6: Production Hardening, Quality Assurance & Day-2 Features
- [x] **Step 6.1: Quality Assurance & Multi-Tier Testing**
  - 21/21 packages unit tested (100% PASS).
  - End-to-end integration pipeline validated ([`test/e2e/pipeline_test.go`](../test/e2e/pipeline_test.go)).
  - Microsecond latency benchmarks recorded ([`test/bench/benchmark_test.go`](../test/bench/benchmark_test.go)).
- [x] **Step 6.2: Day-2 Developer Ergonomics**
  - Integrated `skipper doctor`, `skipper init`, `skipper dev` (hot-reload), `skipper port-forward`, `skipper secret`, and `skipper import compose`.
  - Embedded JSON Schema Draft 2020-12 directly into the binary ([`schemas/embed.go`](../schemas/embed.go)) so `skipper explain --schema` works from any working directory.

### Phase 7: Supply Chain Security, GitOps, Fleet & Autonomous Operations
- [x] **Step 7.1: Supply Chain Security — SBOM Generation & OCI Image Signing**
  - Implemented `SBOMGenerator` ([`pkg/security/sbom.go`](../pkg/security/sbom.go)) supporting CycloneDX 1.5 (`cyclonedx-json`) and SPDX 2.3 (`spdx-json`) via `syft`, `docker sbom`, and a deterministic Go-native fallback (`go.mod`, `package.json`, `requirements.txt`, or OCI image references).
  - Implemented `ImageSigner` ([`pkg/security/sign.go`](../pkg/security/sign.go)) wrapping `cosign sign` and `cosign verify` with key-based and keyless OIDC modes, annotations, and dry-run previews (`skipper sbom`, `skipper sign`).
- [x] **Step 7.2: Ephemeral Debug Sandbox & Session Recording**
  - Implemented `EphemeralSandboxLauncher` ([`pkg/runtime/ephemeral.go`](../pkg/runtime/ephemeral.go)) supporting `kubectl debug` (with `--image`, `--target`, `--copy-to`, `--share-processes`) and Docker namespace-attached debug containers (`skipper debug --ephemeral`).
  - Extended operational memory with structured debug session recording and replay ([`pkg/opmem/session.go`](../pkg/opmem/session.go)).
- [x] **Step 7.3: OpenTelemetry Instrumentation & Canary Health Gates**
  - Implemented `SynthesizeOTelConfig` ([`pkg/synthesizer/otel.go`](../pkg/synthesizer/otel.go)) generating OTLP env vars, Prometheus scrape annotations, and `monitoring.coreos.com/v1` `ServiceMonitor` manifests.
  - Implemented `PrometheusHealthGate` ([`pkg/rollout/metrics.go`](../pkg/rollout/metrics.go)) querying `/api/v1/query` for error rate, P95 latency, and saturation thresholds with `RolloutController`-compatible canary gates (`skipper observe`).
- [x] **Step 7.4: GitOps Export & Sync Engine**
  - Implemented GitOps exporter ([`pkg/gitops/exporter.go`](../pkg/gitops/exporter.go)) generating base Kubernetes manifests, `kustomization.yaml`, environment overlays, and ArgoCD `Application` or FluxCD `GitRepository` + `Kustomization` definitions (`skipper gitops export`).
- [x] **Step 7.5: Multi-Cluster Fleet Context & Drift Detection**
  - Implemented `ContextManager` ([`pkg/cluster/manager.go`](../pkg/cluster/manager.go)) for listing, inspecting, switching kubeconfig contexts, and summarizing fleet reachability (`skipper context`).
  - Implemented `DriftDetector` ([`pkg/cluster/drift.go`](../pkg/cluster/drift.go)) computing deterministic field-level diffs (`Image`, `Replicas`, `Ports`, `Resources`, `Env`, `MissingInLive`, `ExtraInLive`) between desired and live Kubernetes YAML (`skipper context drift`).
- [x] **Step 7.6: Secret Sync with Vault & ExternalSecrets**
  - Implemented `GenerateExternalSecretManifest` and stdlib HTTP `VaultSyncClient` (KV v2 `Push`/`Pull`) ([`pkg/secret/sync.go`](../pkg/secret/sync.go)) generating `external-secrets.io/v1beta1` `SecretStore` + `ExternalSecret` YAML for Vault, AWS Secrets Manager, and GCP Secret Manager (`skipper secret sync`).
- [x] **Step 7.7: Extended Autonomous MCP Operational Tools**
  - Extended the Model Context Protocol server ([`pkg/mcp/server.go`](../pkg/mcp/server.go)) to 10 tools by adding `skipper_rollback`, `skipper_investigate_incident`, `skipper_sbom`, and `skipper_drift_check` alongside the 6 core operational tools.
