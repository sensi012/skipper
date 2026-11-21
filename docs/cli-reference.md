# Skipper CLI Reference Manual

This document provides detailed usage instructions, flags, and examples for all **27 commands** available in the `skipper` CLI.

---

## Global Flags

These persistent flags are available on every `skipper` command:

| Flag | Shorthand | Default | Description |
| :--- | :--- | :--- | :--- |
| `--config <path>` | `-c` | `skipper.yaml` / `app.yaml` | Path to the declarative application specification file |
| `--runtime <name>` | `-r` | `docker` | Target execution runtime: `docker` (local) or `kubernetes` (remote) |
| `--namespace <ns>` | `-n` | `default` | Target Kubernetes namespace (for Kubernetes runtime, manifests, secrets, and drift checks) |
| `--format <fmt>` | `-f` | `text` | Output format: `text` or `json` (machine-parsable output for AI agents and CI) |
| `--version` | `-v` | — | Display the `skipper` CLI version |
| `--help` | `-h` | — | Display help for `skipper` or any subcommand |

---

## 1. Daily Development

### `skipper doctor`
Audits your workstation for container prerequisites (Docker daemon, `kubectl`, `kind`) and scans for port collisions on 80, 443, and 8080. Supports `--format json`.
```bash
$ skipper doctor
==> Running Skipper environment health audit...
  [✓] Docker Engine Daemon           : Running (Version: 26.1.1)
  [✓] Kubernetes CLI (kubectl)       : Client binary available and configured.
  [✓] Kind Local Cluster Tool        : kind v0.33.0
  [✓] Port Availability (80)         : Port 80 is free.
  [✓] Port Availability (443)        : Port 443 is free.
  [✓] Port Availability (8080)       : Port 8080 is free.
==> All critical prerequisites satisfied!

# JSON output for CI or AI agents
$ skipper doctor --format json
```

### `skipper init`
Auto-detects language stack, primary ports, and bootstraps a hardened `skipper.yaml`.

**Flags:**
- `--name <string>` — Workload name (default: current directory name; note: no `-n` shorthand since `-n` is reserved for global `--namespace`)
- `-t`, `--team <string>` — Owning team for governance attribution (default: `platform`)
- `-p`, `--port <int>` — Primary service port (auto-detected if omitted)

```bash
$ skipper init --name payment-api --team checkout --port 8080
==> Successfully generated 'skipper.yaml' for [payment-api] (Port: 8080, Team: checkout)!
```

### `skipper dev`
Watches local files, debounces changes, and automatically synchronizes and rebuilds containers in real-time.
```bash
$ skipper dev
==> [Skipper Dev] Watching . for code modifications...
==> [Skipper Dev] Application 'payment-api' running at status [Running]
==> [Change Detected: main.go] Synchronizing & rebuilding...
```

### `skipper build`
Auto-detects Go, Node.js, Python, Rust, Java, or custom Dockerfiles and builds standard OCI images without manual Dockerfile authoring. Records build mutations into operational memory.
```bash
$ skipper build
==> Auto-detected stack: [go] (Files: go.mod)
==> Synthesizing optimal multi-stage container build for [go]...
==> Successfully built and tagged OCI image: payment-api:latest
```

### `skipper up` / `skipper down`
Starts or stops the container workload on the configured runtime (`-r docker` or `-r kubernetes`).
```bash
$ skipper up
$ skipper up -r kubernetes -n staging
$ skipper down
$ skipper down payment-api
```

### `skipper status` / `skipper logs`
Inspect live execution state, replica counts, and stream container stdout/stderr.

**`skipper logs [name]` Flags:**
- `-f`, `--follow` — Stream live logs continuously (default: `false`)
- `-t`, `--tail <int>` — Number of recent log lines to display (default: `50`)

```bash
$ skipper status
$ skipper status payment-api
$ skipper logs -f --tail 100
```

---

## 2. Secrets & Networking

### `skipper secret`
Securely manage local environment secrets, export Kubernetes `v1/Secret` manifests, sync with HashiCorp Vault KV v2, and generate ExternalSecrets Operator manifests.

#### `skipper secret set` / `get` / `export`
```bash
# Save secret locally
$ skipper secret set DB_PASSWORD=supersecret
==> Secret 'DB_PASSWORD' saved successfully.

# Read secret
$ skipper secret get DB_PASSWORD
DB_PASSWORD=supersecret

# Export as Kubernetes v1 Secret manifest (respects global -n/--namespace)
$ skipper secret export -n prod
apiVersion: v1
kind: Secret
metadata:
  name: skipper-secrets
  namespace: prod
type: Opaque
data:
  DB_PASSWORD: c3VwZXJzZWNyZXQ=
```

#### `skipper secret sync`
Sync secrets with HashiCorp Vault KV v2 (`push` / `pull`) or generate Kubernetes ExternalSecrets Operator (`external-secrets.io/v1beta1`) `SecretStore` + `ExternalSecret` YAML manifests for `vault`, `aws`, or `gcp`.

**Flags:**
- `--provider <vault|aws|gcp>` — Secret backend provider (default: `vault`)
- `--vault-addr <url>` — HashiCorp Vault server URL (default: `$VAULT_ADDR`)
- `--vault-token <token>` — HashiCorp Vault auth token (default: `$VAULT_TOKEN`)
- `--vault-mount <path>` — Vault KV v2 mount path (default: `secret`)
- `--vault-path <path>` — Remote secret path in Vault/AWS/GCP (default: `skipper/app`)
- `--external-secret` — Output ExternalSecrets Operator `SecretStore` + `ExternalSecret` YAML (forces `--direction manifest`)
- `--direction <push|pull|manifest>` — Sync mode (default: `manifest`)
- `--name <string>` — `ExternalSecret` resource name (default: `skipper-secrets`)
- `--keys <k1,k2,...>` — Comma-separated secret keys to include in the manifest (defaults to locally stored keys)
- `--aws-region <region>` — AWS Secrets Manager region for `--provider aws` (default: `us-east-1`)
- `--gcp-project <id>` — GCP Project ID for `--provider gcp`
- Supports global `--format json` and `-n`/`--namespace`

```bash
# Generate ExternalSecrets manifest for HashiCorp Vault
$ skipper secret sync --provider vault --vault-addr https://vault.internal:8200 \
    --vault-path prod/payment-api --external-secret -n prod

# Generate ExternalSecrets manifest for AWS Secrets Manager
$ skipper secret sync --provider aws --aws-region us-west-2 --vault-path prod/payment-api \
    --keys DB_URL,API_KEY --external-secret --format json

# Push local secrets to HashiCorp Vault KV v2
$ skipper secret sync --direction push --vault-addr https://vault.internal:8200 \
    --vault-token "$VAULT_TOKEN" --vault-mount secret --vault-path prod/payment-api

# Pull secrets from HashiCorp Vault KV v2 into local store
$ skipper secret sync --direction pull --vault-addr https://vault.internal:8200 \
    --vault-token "$VAULT_TOKEN" --vault-mount secret --vault-path prod/payment-api
```

### `skipper port-forward`
Establishes a bi-directional network tunnel from localhost to a remote Kubernetes deployment. Uses the global `-n`/`--namespace` flag.
```bash
$ skipper port-forward sample-service 8080:8080 -n staging
==> Establishing port-forward to sample-service [Local :8080 -> Remote :8080] in namespace 'staging'...
Forwarding from 127.0.0.1:8080 -> 8080
```

### `skipper import compose`
Translates legacy `docker-compose.yaml` (or `compose.yaml`) files into native `skipper.yaml` with production-hardened defaults.
```bash
$ skipper import compose docker-compose.yaml
==> Successfully transpiled 'docker-compose.yaml' into 'skipper.yaml' (Service: web)!
```

---

## 3. Production Delivery, GitOps & Multi-Cluster Fleet

### `skipper manifest`
Compiles `skipper.yaml` into complete, production-hardened Kubernetes manifests (`Deployment`, `Service`, `NetworkPolicy`, `PodDisruptionBudget`).

**Flags:**
- `-o`, `--output <path>` — Write output to file path instead of stdout
- Uses global `-n`/`--namespace` and `-c`/`--config`

```bash
$ skipper manifest -n prod -o deploy.yaml
==> Synthesized production manifests written to: deploy.yaml
```

### `skipper deploy`
Evaluates platform governance policies and executes progressive rollout strategies with automated health validation gates. Records rollout mutations into operational memory.

**Flags:**
- `-s`, `--strategy <canary|blue-green>` — Rollout strategy (default: `canary`)
- `--auto-rollback` — Automatically rollback on health gate failure (default: `true`)

```bash
# Canary rollout with automatic rollback
$ skipper deploy --strategy canary --auto-rollback

# Blue-Green rollout
$ skipper deploy -s blue-green --auto-rollback
```

### `skipper gitops export`
Generates a complete Kustomize repository layout (`base/` manifests + `base/kustomization.yaml` + `overlays/<env>/{kustomization,patch}.yaml`) along with ArgoCD (`argoproj.io/v1alpha1` `Application`) or FluxCD (`source.toolkit.fluxcd.io/v1` `GitRepository` + `kustomize.toolkit.fluxcd.io/v1` `Kustomization`) sync manifests.

**Flags:**
- `--engine <argocd|flux>` — Target GitOps engine (default: `argocd`)
- `--repo <url>` — Target GitOps repository URL (default: `https://github.com/example/gitops-repo.git`)
- `--branch <branch>` — Target Git branch (default: `main`)
- `--env <env>` — Target environment overlay, e.g. `staging` or `prod` (default: `prod`)
- `--target-path <path>` — Optional path inside the Git repository (default: `overlays/<env>`)
- `-o`, `--output <dir>` — Output directory to write generated GitOps files
- Supports global `--format json`, `-n`/`--namespace`, and `-c`/`--config`

```bash
# Export ArgoCD GitOps bundle to ./gitops-out
$ skipper gitops export --engine argocd --repo https://github.com/acme/fleet.git \
    --branch main --env prod -n payments --output ./gitops-out

# Export FluxCD GitOps bundle as structured JSON
$ skipper gitops export --engine flux --repo https://github.com/acme/fleet.git \
    --env staging --target-path apps/payments/overlays/staging --format json
```

### `skipper context`
Manages multi-cluster Kubernetes fleet contexts and detects deterministic field-level configuration drift between desired and live Kubernetes manifests.

**Subcommands:**
- `skipper context` — Show fleet summary (active context, cluster count, reachability)
- `skipper context list` — List all configured kubeconfig contexts
- `skipper context current` — Display the active Kubernetes context
- `skipper context use <name>` — Switch the active Kubernetes context
- `skipper context drift` — Detect drift (`Image`, `Replicas`, `Ports`, `Resources`, `Env`, `MissingInLive`, `ExtraInLive`)

**`skipper context drift` Flags:**
- `--context <name>` — Target kubeconfig context for live cluster drift check
- `--desired <file>` — Path to desired Kubernetes YAML manifest (for file-based drift comparison)
- `--live <file>` — Path to live Kubernetes YAML manifest (for file-based drift comparison)
- Supports global `--format json`, `-n`/`--namespace`, and `-c`/`--config`

```bash
# Inspect multi-cluster fleet status
$ skipper context --format json

# List, inspect, and switch kubeconfig contexts
$ skipper context list
$ skipper context current
$ skipper context use prod-us-east-1

# Compare desired vs live manifests offline
$ skipper context drift --desired desired.yaml --live live.yaml --format json

# Detect live drift against a cluster context using skipper.yaml
$ skipper context drift --context prod-us-east-1 -n payments
```

### `skipper local`
Manages ephemeral local development Kubernetes clusters using Kind.

**Flags:**
- `--name <string>` — Local cluster name (default: `skipper-dev`)

```bash
$ skipper local init --name my-dev-cluster
$ skipper local status
$ skipper local destroy
```

---

## 4. Observability, Diagnostics, Security & Governance

### `skipper observe`
Evaluates live Prometheus/OTel canary SLO health gates (error rate, P95 latency, saturation via `/api/v1/query`) or synthesizes OpenTelemetry instrumentation patches (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `prometheus.io/*` annotations) and a Prometheus `ServiceMonitor` (`monitoring.coreos.com/v1`).

**Flags:**
- `--prometheus-url <url>` — Prometheus/OTel HTTP query endpoint (default: `http://localhost:9090`)
- `--max-error-rate <float>` — Maximum allowed 5xx error ratio, e.g. `0.01` = 1% (default: `0.01`)
- `--max-p95-ms <float>` — Maximum allowed P95 latency in milliseconds (default: `500.0`)
- `--max-saturation <float>` — Maximum allowed CPU/memory saturation ratio (default: `0.85`)
- `--otel-manifest` — Synthesize OpenTelemetry env/annotations and Prometheus `ServiceMonitor` YAML
- `--otlp-endpoint <url>` — OTLP exporter endpoint for `--otel-manifest` (default: `http://otel-collector:4317`)
- Supports global `--format json`, `-n`/`--namespace`, and `-c`/`--config`

```bash
# Synthesize OTel instrumentation patch + ServiceMonitor YAML
$ skipper observe --otel-manifest --otlp-endpoint http://otel-collector.monitoring:4317 -n prod

# Evaluate live Prometheus canary health gates
$ skipper observe payment-api --prometheus-url http://prometheus.monitoring:9090 \
    --max-error-rate 0.01 --max-p95-ms 250 --max-saturation 0.80 --format json
```

### `skipper diagnose`
Executes the deterministic 8-detector rule matrix against live container logs and operational memory context (detects OOM kills, CrashLoopBackOff, probe failures, network drops, etc.). Supports `--format json`.
```bash
$ skipper diagnose payment-api
==> Evaluating deterministic diagnostic matrix & operational context on 'payment-api'...
[DIAGNOSIS COMPLETE: 0 ANOMALIES DETECTED] Workload appears healthy.

$ skipper diagnose payment-api -f json
```

### `skipper debug`
Attaches an interactive shell or an isolated ephemeral debugging sandbox container (`kubectl debug` on Kubernetes, `docker run --pid=container:... --network=container:...` on Docker) and records the debug session into operational memory for replay.

**Flags:**
- `--ephemeral` — Launch an isolated ephemeral debug sandbox container (default: `false`)
- `--debug-image <image>` — Container image for ephemeral debug sandbox (default: `nicolaka/netshoot:latest`)
- `--target-container <name>` — Target container name inside pod for process namespace sharing
- `--copy-pod` — Debug a copy of the target pod (Kubernetes only, default: `false`)
- `--share-processes` — Share process namespace with target container (default: `true`)
- `--dry-run` — Print resolved sandbox command without executing (default: `false`)
- Supports global `-r`/`--runtime`, `-n`/`--namespace`, and `--format json` (with `--ephemeral`)

```bash
# Standard interactive debug session
$ skipper debug payment-api

# Ephemeral Kubernetes debug sandbox (dry-run preview)
$ skipper debug payment-api --ephemeral -r kubernetes -n prod \
    --debug-image nicolaka/netshoot:latest --copy-pod --share-processes --dry-run

# Ephemeral Docker debug sandbox with JSON output
$ skipper debug payment-api --ephemeral --dry-run --format json
```

### `skipper time-travel`
Replays chronological mutations, build events, rollouts, and debug sessions from the immutable operational memory ledger.

**Flags:**
- `-w`, `--window <duration>` — Retrospective lookback window, e.g. `15m`, `1h`, `24h` (default: `30m`)

```bash
$ skipper time-travel payment-api --window 1h
```

### `skipper scan`
Runs pre-deployment vulnerability and misconfiguration audits on container images using Docker Scout or Trivy.
```bash
$ skipper scan payment-api:latest
```

### `skipper sbom`
Generates a CycloneDX 1.5 or SPDX 2.3 Software Bill of Materials (SBOM) for a project directory or OCI image. Uses `syft` or `docker sbom` when available, with a deterministic Go-native fallback parsing `go.mod`, `package.json`, `requirements.txt`, or OCI image metadata.

**Flags:**
- `--target <path|image>` — Target directory or OCI image reference (default: `.`)
- `--sbom-format <cyclonedx|spdx>` — SBOM specification format (default: `cyclonedx`)
- `-o`, `--output <path>` — Write SBOM JSON to a file path
- Supports global `--format json`

```bash
# Generate CycloneDX 1.5 SBOM for the current directory
$ skipper sbom --target . --sbom-format cyclonedx --output sbom.cdx.json

# Generate SPDX 2.3 SBOM as JSON summary
$ skipper sbom --target . --sbom-format spdx --format json
```

### `skipper sign`
Signs or verifies OCI container images using Sigstore Cosign (keyed via `--key` or keyless OIDC). Supports `--dry-run` to inspect the exact Cosign command without requiring Cosign to be installed.

**Flags:**
- `--image <ref>` — OCI image reference to sign or verify (can also be passed as positional arg or loaded from `skipper.yaml`)
- `--key <ref>` — Path or KMS URI to signing/verification key (keyless OIDC if omitted)
- `--verify` — Verify an existing image signature instead of signing (default: `false`)
- `--dry-run` — Print resolved Cosign command without executing (default: `false`)
- `--certificate-identity <id>` — Expected OIDC certificate identity (for keyless `--verify`)
- `--certificate-issuer <issuer>` — Expected OIDC certificate issuer (for keyless `--verify`)
- Supports global `--format json`

```bash
# Keyless sign (dry-run)
$ skipper sign --image ghcr.io/acme/payment-api:v1.2.0 --dry-run

# Keyed sign
$ skipper sign --image ghcr.io/acme/payment-api:v1.2.0 --key cosign.key

# Verify keyless signature with OIDC identity checks
$ skipper sign --image ghcr.io/acme/payment-api:v1.2.0 --verify \
    --certificate-identity https://github.com/acme/repo/.github/workflows/release.yml@refs/tags/v1.2.0 \
    --certificate-issuer https://token.actions.githubusercontent.com --format json
```

### `skipper policy-check`
Validates `skipper.yaml` against organizational compliance rules (team ownership, non-root execution, resource limits). Supports `--format json`.
```bash
$ skipper policy-check
[POLICY AUDIT PASSED] AppSpec complies with all enterprise platform rules.
```

### `skipper rightsize`
Compares requested CPU/memory against actual P95 utilization to calculate monthly cloud cost savings. Supports `--format json`.
```bash
$ skipper rightsize --format json
```

---

## 5. AI Agent & MCP Integrations

### Global `--format json` Flag
Diagnostic, audit, observability, supply-chain, GitOps, fleet, and secret commands support machine-parsable JSON output tailored for LLM tool calling and CI automation:
```bash
$ skipper doctor --format json
$ skipper diagnose payment-api -f json
$ skipper policy-check -f json
$ skipper rightsize -f json
$ skipper sbom --sbom-format cyclonedx -f json
$ skipper observe payment-api -f json
$ skipper gitops export --engine argocd -f json
$ skipper context drift --desired desired.yaml --live live.yaml -f json
```

### `skipper mcp`
Launches an official Model Context Protocol (MCP) server over standard I/O (`stdio`), allowing AI agents (Claude Desktop, Cursor, Antigravity, Windsurf) to invoke **10 built-in Skipper operational tools**:

| # | Tool Name | Parameters | Description |
| :--- | :--- | :--- | :--- |
| 1 | `skipper_doctor` | *(none)* | Audits Docker daemon, `kubectl`, Kind CLI, and port conflicts |
| 2 | `skipper_init` | `name`, `port` | Inspects the project and generates a hardened `skipper.yaml` |
| 3 | `skipper_policy_check` | `configPath` | Validates `skipper.yaml` against compliance and security policies |
| 4 | `skipper_manifest` | `configPath`, `namespace` | Compiles `skipper.yaml` into hardened Kubernetes YAML manifests |
| 5 | `skipper_diagnose` | `workload`*, `logs`* | Runs the 8-detector deterministic matrix on workload logs |
| 6 | `skipper_rightsize` | `configPath` | Analyzes CPU/memory allocations vs P95 usage for FinOps savings |
| 7 | `skipper_rollback` | `workload`*, `revision`, `reason`, `namespace` | Triggers a deterministic workload rollback and records it in operational memory |
| 8 | `skipper_investigate_incident` | `workload`*, `logs`*, `namespace`, `windowMinutes` | Correlates operational memory timeline + debug sessions with 8-detector diagnostics into a unified incident report |
| 9 | `skipper_sbom` | `target`, `format`, `outputPath` | Generates a CycloneDX 1.5 or SPDX 2.3 SBOM for a directory or OCI image |
| 10 | `skipper_drift_check` | `desiredYaml`, `liveYaml`, `configPath`, `namespace`, `context` | Detects configuration drift between desired/live YAML or against a live cluster context |

*\* Required parameters.*

```bash
$ skipper mcp
```

### `skipper explain`
Outputs an operational playbook and instruction grammar designed for inclusion in an AI agent's system prompt or scratchpad.

**Flags:**
- `--schema` — Output the embedded formal JSON Schema (Draft 2020-12) for `skipper.yaml` (embedded directly in the binary, so it works from any working directory)
- Supports global `--format json` for structured JSON playbook output

```bash
# Human/LLM markdown playbook
$ skipper explain

# Structured JSON playbook
$ skipper explain --format json

# Embedded JSON Schema Draft 2020-12 (works from any directory)
$ skipper explain --schema
```
