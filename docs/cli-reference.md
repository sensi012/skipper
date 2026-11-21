# Skipper CLI Reference Manual

This document provides detailed usage instructions and flags for all 16 commands available in the `skipper` CLI.

---

## 1. Daily Development

### `skipper doctor`
Audits your workstation for container prerequisites (Docker daemon, `kubectl`, `kind`) and scans for port collisions on 80, 443, and 8080.
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
```

### `skipper init`
Auto-detects language stack, primary ports, and bootstraps a hardened `skipper.yaml`.
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
Auto-detects Go, Node.js, Python, Rust, or custom Dockerfiles and builds standard OCI images without manual Dockerfile authoring.
```bash
$ skipper build
==> Auto-detected stack: [go] (Files: go.mod)
==> Synthesizing optimal multi-stage container build for [go]...
==> Successfully built and tagged OCI image: payment-api:latest
```

### `skipper up` / `skipper down`
Starts or terminates the container workload locally via Docker or local Kubernetes.
```bash
$ skipper up
$ skipper down
```

### `skipper logs` / `skipper status`
Inspect live execution state, replica counts, and stream container stdout/stderr.
```bash
$ skipper status
$ skipper logs -f
```

---

## 2. Secrets & Networking

### `skipper secret`
Securely manage local environment secrets and export directly into base64-encoded Kubernetes `v1/Secret` manifests.
```bash
# Save secret
$ skipper secret set DB_PASSWORD=supersecret
==> Secret 'DB_PASSWORD' saved successfully.

# Read secret
$ skipper secret get DB_PASSWORD
DB_PASSWORD=supersecret

# Export as Kubernetes Secret manifest
$ skipper secret export
apiVersion: v1
kind: Secret
metadata:
  name: skipper-secrets
  namespace: default
type: Opaque
data:
  DB_PASSWORD: c3VwZXJzZWNyZXQ=
```

### `skipper port-forward`
Establishes a bi-directional network tunnel from localhost to a remote Kubernetes deployment.
```bash
$ skipper port-forward sample-service 8080:8080
==> Establishing port-forward to sample-service [Local :8080 -> Remote :8080] in namespace 'default'...
Forwarding from 127.0.0.1:8080 -> 8080
```

### `skipper import compose`
Translates legacy `docker-compose.yaml` files into native `skipper.yaml` with production-hardened defaults.
```bash
$ skipper import compose docker-compose.yaml
==> Successfully transpiled 'docker-compose.yaml' into 'skipper.yaml' (Service: web)!
```

---

## 3. Production Delivery & Manifests

### `skipper manifest`
Compiles `skipper.yaml` into complete, production-hardened Kubernetes manifests (Deployment, Service, NetworkPolicy, PodDisruptionBudget).
```bash
$ skipper manifest -o deploy.yaml
==> Synthesized production manifests written to: deploy.yaml
```

### `skipper deploy`
Executes progressive rollout strategies with automated health validation gates.
```bash
# Canary rollout
$ skipper deploy --strategy=canary --auto-rollback

# Blue-Green rollout
$ skipper deploy --strategy=blue-green --auto-rollback
```

### `skipper local`
Manages ephemeral local development clusters using Kind.
```bash
$ skipper local init
$ skipper local status
$ skipper local destroy
```

---

## 4. Diagnostics, Security & Governance

### `skipper diagnose`
Executes the deterministic 8-detector rule matrix against live container logs and telemetry (detects OOM kills, CrashLoopBackOff, probe failures, network drops, etc.).
```bash
$ skipper diagnose payment-api
==> Evaluating deterministic diagnostic matrix on 'payment-api'...
[DIAGNOSIS COMPLETE: 0 ANOMALIES DETECTED] Workload appears healthy.
```

### `skipper debug`
Attaches an ephemeral forensic container directly to a running pod for live troubleshooting.
```bash
$ skipper debug payment-api
```

### `skipper time-travel`
Replays chronological mutations, build events, and telemetry from the immutable operational ledger.
```bash
$ skipper time-travel --window 1h
```

### `skipper scan`
Runs pre-deployment security audits on container images using Docker Scout or Trivy.
```bash
$ skipper scan payment-api:latest
```

### `skipper policy-check`
Validates `skipper.yaml` against organizational compliance rules (team ownership, non-root execution, resource limits).
```bash
$ skipper policy-check
[POLICY AUDIT PASSED] AppSpec complies with all enterprise platform rules.
```

### `skipper rightsize`
Compares requested CPU/memory against actual P95 utilization to calculate monthly cloud cost savings.
```bash
$ skipper rightsize
```

---

## 4. AI Agent & Tooling Integrations

### Global `--format json` Flag
Any CLI diagnostic or audit command supports machine-parsable JSON output, specifically tailored for LLM function calling and automated workflows:
```bash
$ skipper doctor --format json
$ skipper diagnose payment-api -f json
$ skipper policy-check -f json
$ skipper rightsize -f json
```

### `skipper mcp`
Launches an official Model Context Protocol (MCP) server over standard I/O (`stdio`). This allows AI agents (Claude Desktop, Cursor, Antigravity, Windsurf) to directly invoke Skipper as tools:
- `skipper_doctor`: Workspace health inspection.
- `skipper_init`: Language stack inspection & specification generation.
- `skipper_policy_check`: Enterprise compliance auditing.
- `skipper_manifest`: Synthesis of standard Kubernetes manifests.
- `skipper_diagnose`: Deterministic crash triage with actionable remediation commands.
- `skipper_rightsize`: Compute optimization & FinOps recommendations.

```bash
$ skipper mcp
```

### `skipper explain`
Outputs an operational playbook and grammar designed to be included in an AI agent's system prompt or scratchpad:
```bash
$ skipper explain
```

To retrieve the strict JSON Schema (Draft 2020-12) describing `skipper.yaml`:
```bash
$ skipper explain --schema
```
