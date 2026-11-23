# Skipper CLI Reference Manual

This document provides detailed usage instructions, flags, and examples for all **28 commands** available in the `skipper` CLI.

---

## Global Flags

These persistent flags are available on every `skipper` command:

| Flag | Shorthand | Default | Description |
| :--- | :--- | :--- | :--- |
| `--config <path>` | `-c` | `skipper.yaml` / `app.yaml` | Path to the declarative application specification file |
| `--runtime <name>` | `-r` | `docker` | Target execution runtime: `docker` (local) or `kubernetes` (remote) |
| `--namespace <ns>` | `-n` | `default` | Target Kubernetes namespace (for Kubernetes runtime, manifests, secrets, rollbacks, and drift checks) |
| `--format <fmt>` | `-f` | `text` | Output format: `text` or `json` (strict machine-parsable JSON on stdout for AI agents and CI) |
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
Auto-detects language stack, primary ports, and bootstraps a hardened `skipper.yaml`. Supports `--format json` (emits `{"file","spec"}`).

**Flags:**
- `--name <string>` — Workload name (default: current directory name; note: no `-n` shorthand since `-n` is reserved for global `--namespace`)
- `-t`, `--team <string>` — Owning team for governance attribution (default: `platform`)
- `-p`, `--port <int>` — Primary service port (auto-detected if omitted)

```bash
$ skipper init --name payment-api --team checkout --port 8080
==> Successfully generated 'skipper.yaml' for [payment-api] (Port: 8080, Team: checkout)!

$ skipper init --name payment-api --format json
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
Builds OCI-compliant container images using automatic mode resolution across single-service, Docker Compose, and monorepo subdirectory layouts, with intelligent multi-stage Dockerfile synthesis when no `Dockerfile` is present. Records build mutations into operational memory.

**Build Modes (resolution order):**
1. **Explicit Compose (`--compose <path>`):** Builds all services with a `build:` block (sequentially in dependency order) and skips prebuilt `image:` services.
2. **Explicit Subdirectory Discovery (`--discover`):** Scans subdirectories up to 2 levels deep for Dockerfiles or stack markers (`go.mod`, `package.json`, `requirements.txt`, `pyproject.toml`, `Cargo.toml`, `pom.xml`, `build.gradle`).
3. **Single-Service (`skipper.yaml` / `app.yaml` / `-c`):** Classic single-app build using CNB (`pack`), Docker BuildKit, or synthesized multi-stage Dockerfiles.
4. **Auto-Detected Compose:** When no `skipper.yaml` exists, automatically looks for `compose.yaml`, `compose.yml`, `docker-compose.yaml`, or `docker-compose.yml`.
5. **Auto-Discover Fallback:** If neither `skipper.yaml` nor a compose file exists, automatically scans subdirectories (depth 2) and builds discovered services.

**Smart Multi-Stage Dockerfile Synthesis (when no `Dockerfile` exists):**
- **Node.js (`package.json`):**
  - *SPA / Static Frontend*: Detects `scripts.build` combined with a frontend bundler/framework (`vite`, `react-scripts`, `@angular/cli`, `vue`, `@vue/cli-service`, `svelte`, `parcel`, `gatsby`) without an explicit `node <file>` start script. Synthesizes a 2-stage build (`node:20-alpine` running `npm ci || npm install` + `npm run build` -> `nginx:alpine` serving `/app/dist` or `/app/build` for CRA with SPA `try_files $uri $uri/ /index.html;` routing).
  - *Backend / API*: Extracts a validated entrypoint from `scripts.start` (`node <file>`) or `main`, falling back to probing `server.js` -> `app.js` -> `index.js`. When `scripts.build` is present (e.g., TypeScript), runs `npm ci || npm install`, `npm run build`, and `npm prune --omit=dev || true` before copying to `gcr.io/distroless/nodejs20-debian12:nonroot` with `ENTRYPOINT ["/nodejs/bin/node", "<entrypoint>"]`.
- **Python (`requirements.txt` or `pyproject.toml`):**
  - Auto-detects the target Python version (`3.8`–`3.20`) from `.python-version` or `pyproject.toml` `requires-python` (default `python:3.12-slim`), and runs fail-fast `mkdir -p /install && pip install ...` without masking pip errors.
  - Supports `pyproject.toml`-only projects cleanly (`pip install --no-cache-dir --prefix=/install .`) as well as `requirements.txt`.
  - Automatically detects frameworks and entrypoints (`main.py` -> `app.py` -> `server.py` -> `wsgi.py`, plus Django `<pkg>/wsgi.py`):
    - `fastapi` / `uvicorn` -> `CMD ["uvicorn", "<mod>:app", "--host", "0.0.0.0", "--port", "8000"]`
    - `gunicorn` -> `CMD ["gunicorn", "--bind", "0.0.0.0:8000", "<mod>:<callable>"]` (e.g., `<pkg>.wsgi:application` for Django)
    - Default -> `CMD ["python", "<file>"]`
- **Go (`go.mod`):** Synthesizes minimal hardened multi-stage builds targeting `gcr.io/distroless/static-debian12:nonroot`.
- **Rust & Java:** Auto-detected and built via your existing `Dockerfile` or Cloud Native Buildpacks (`pack`).

**Flags:**
- `--compose <path>` — Path to a Docker Compose file for multi-service builds
- `--discover` — Scan subdirectories (depth 2) and build discovered services
- `--service <name[,name...]>` — Filter to specific service(s) in compose or discover mode (repeatable or comma-separated)
- `-t`, `--tag <image:tag>` — Override target OCI image tag (single-service builds or when filtered to 1 service via `--service`)
- `--save` — Persist the built image tag into `skipper.yaml` (single-service mode only)
- Supports global `--format json` (single-service emits `{"service","image","status","saved"}`; multi-service emits `{"project","mode","results":[...]}`; build logs route to stderr in JSON mode)

```bash
# Single-service build (with skipper.yaml)
$ skipper build
==> Auto-detected stack: [go] (Files: go.mod)
==> Synthesizing optimal multi-stage container build for [go]...
==> Successfully built and tagged OCI image: payment-api:latest

$ skipper build -t ghcr.io/my-org/payment-api:v1.2.3 --save --format json

# Multi-service Compose repo (without skipper.yaml)
$ skipper build
==> No skipper.yaml found; using docker-compose.yml (project: three-tier, 3 services)
==> [1/3] Skipping service 'db' (prebuilt image postgres:16-alpine)
==> [2/3] Building service 'backend' (context: backend)
...
==> [3/3] Building service 'frontend' (context: frontend)
...

SERVICE              STATUS     IMAGE                                DURATION
db                   skipped    postgres:16-alpine                   -
backend              built      three-tier-backend:latest            1842ms
frontend             built      three-tier-frontend:latest           612ms

# Build only a single service from Compose or discovered monorepo
$ skipper build --service backend
$ skipper build --discover --service frontend --format json
```

### `skipper up`
Starts a single-service workload (`-r docker` or `-r kubernetes`) or a multi-service Docker Compose stack (`docker compose up -d`). Records `MUTATION` events into operational memory.

**Mode Resolution:**
1. Explicit `--compose <path>`: Compose mode (requires `-r docker`; returns an error if `-r kubernetes` is set).
2. Explicit `-c <path>` or local `skipper.yaml` / `app.yaml`: Single-service mode (rejects `--service`).
3. No `skipper.yaml` + `-r docker` + local Compose file (`compose.yaml`, `compose.yml`, `docker-compose.yaml`, `docker-compose.yml`): Automatic Compose mode.

**Flags:**
- `-i`, `--image <image:tag>` — Override target OCI image tag for single-service runs
- `--compose <path>` — Path to a Docker Compose file for multi-service startup
- `--service <name[,name...]>` — Filter to specific Compose service(s) (repeatable or comma-separated; validated against the Compose project)
- `--build` — Rebuild Compose service images before starting (`docker compose up -d --build`)
- Supports global `--format json` (routes progress to stderr; Compose mode emits `{"mode":"compose","project":"...","file":"...","services":[...]}`)

```bash
# Single-service launch
$ skipper up
$ skipper up -i ghcr.io/my-org/payment-api:v1.2.3
$ skipper up -r kubernetes -n staging

# Automatic multi-service Compose launch (when docker-compose.yml is present without skipper.yaml)
$ skipper up --build
==> No skipper.yaml found; using docker-compose.yml (project: three-tier)
==> Started Compose project 'three-tier' from docker-compose.yml.
SERVICE         STATE      IMAGE                      PORTS                  STATUS
db              running    postgres:16-alpine         0.0.0.0:5432->5432/tcp Up 4 seconds
backend         running    three-tier-backend:latest  0.0.0.0:8080->8080/tcp Up 3 seconds
frontend        running    three-tier-frontend:latest 0.0.0.0:3000->80/tcp   Up 2 seconds

# Explicit Compose file with service filtering and strict JSON output
$ skipper up --compose docker-compose.yml --service db,backend --format json
```

### `skipper down`
Stops and removes a single-service workload (`skipper down [name]`, running `docker stop` + `docker rm -f`) or tears down a Docker Compose stack (`docker compose down --remove-orphans`). Records `MUTATION` events into operational memory.

**Mode Resolution:**
1. Explicit `--compose <path>`: Compose mode (requires `-r docker`).
2. Positional `[name]` arg, explicit `-c`, or local `skipper.yaml` / `app.yaml`: Single-service mode.
3. No `skipper.yaml` + `-r docker` + local Compose file: Automatic Compose mode.

**Flags:**
- `--compose <path>` — Path to a Docker Compose file for multi-service teardown
- `-v`, `--volumes` — Remove named volumes declared in the Compose file (`docker compose down -v --remove-orphans`)
- Supports global `--format json` (Compose mode emits `{"mode":"compose","project":"...","file":"...","status":"STOPPED","volumesRemoved":<bool>}`)

```bash
# Single-service stop
$ skipper down
$ skipper down payment-api

# Compose stack teardown (auto-detected or explicit)
$ skipper down
$ skipper down --compose docker-compose.yml --volumes --format json
```

### `skipper status`
Inspects live execution state and replica counts for a single workload (`skipper status [name]`) or prints a multi-service status table (`SERVICE`, `STATE`, `IMAGE`, `PORTS`, `STATUS`) for a Docker Compose project.

**Flags:**
- `--compose <path>` — Path to a Docker Compose file
- `--service <name[,name...]>` — Filter status output to specific Compose service(s) (repeatable or comma-separated)
- Supports global `--format json` (Compose mode emits `{"mode":"compose","project":"...","file":"...","services":[...]}` with a non-nil array)

```bash
# Single-service status
$ skipper status
$ skipper status payment-api --format json

# Multi-service Compose status
$ skipper status
SERVICE         STATE      IMAGE                      PORTS                  STATUS
db              running    postgres:16-alpine         0.0.0.0:5432->5432/tcp Up 2 minutes
backend         running    three-tier-backend:latest  0.0.0.0:8080->8080/tcp Up 2 minutes

$ skipper status --compose docker-compose.yml --service backend --format json
```

### `skipper logs`
Streams container stdout/stderr from a local Docker container or Kubernetes pod (`skipper logs [name]`).

**Flags:**
- `--follow` — Stream live logs continuously (default: `false`; note: no `-f` shorthand since `-f` is reserved for global `--format`)
- `-t`, `--tail <int>` — Number of recent log lines to display (default: `50`)

```bash
$ skipper logs
$ skipper logs payment-api --follow --tail 100
```

---

## 2. Secrets & Networking

### `skipper secret`
Securely manage local environment secrets, export Kubernetes `v1/Secret` manifests, sync with HashiCorp Vault KV v2, and generate ExternalSecrets Operator manifests. All subcommands (`set`, `get`, `export`, `sync`) support global `--format json`.

#### `skipper secret set` / `get` / `export`
```bash
# Save secret locally
$ skipper secret set DB_PASSWORD=supersecret
==> Secret 'DB_PASSWORD' saved successfully.

# Read secret (or JSON)
$ skipper secret get DB_PASSWORD
DB_PASSWORD=supersecret
$ skipper secret get DB_PASSWORD --format json

# Export as Kubernetes v1 Secret manifest (respects global -n/--namespace and --format json)
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
Translates `compose.yaml`, `compose.yml`, `docker-compose.yaml`, or `docker-compose.yml` files into native `skipper.yaml` with production-hardened defaults (supports long-syntax ports/volumes, build args/target, and environment lookups). Supports `--format json` (`{"sourceFile","targetFile","service","spec"}`).

**Flags:**
- `--service <name>` — Specific service in the Compose file to transpile (if omitted, selects the first buildable service alphabetically)

```bash
$ skipper import compose
==> Successfully transpiled 'docker-compose.yml' into 'skipper.yaml' (Service: backend)!
Note: Compose file has 3 services (backend, db, frontend); imported 'backend'. Use --service to choose, or run 'skipper build' directly for all services.

$ skipper import compose docker-compose.yml --service frontend --format json
```

---

## 3. Production Delivery, Rollbacks, GitOps & Multi-Cluster Fleet

### `skipper manifest`
Compiles `skipper.yaml` into complete, production-hardened Kubernetes manifests (`Deployment`, `Service`, `NetworkPolicy`, `PodDisruptionBudget`). Supports `--format json` (`{"workload","namespace","outputFile","manifest"}`).

**Flags:**
- `-o`, `--output <path>` — Write output to file path instead of stdout
- Uses global `-n`/`--namespace` and `-c`/`--config`

```bash
$ skipper manifest -n prod -o deploy.yaml
==> Synthesized production manifests written to: deploy.yaml

$ skipper manifest -n prod --format json
```

### `skipper deploy`
Evaluates platform governance policies and executes progressive rollout strategies with automated health validation gates. Records rollout mutations into operational memory. Supports `--format json` (routes progress to stderr and emits structured rollout status JSON on stdout).

**Flags:**
- `-s`, `--strategy <canary|blue-green>` — Rollout strategy (default: `canary`)
- `--auto-rollback` — Automatically rollback on health gate failure (default: `true`)

```bash
# Canary rollout with automatic rollback
$ skipper deploy --strategy canary --auto-rollback

# Blue-Green rollout with JSON output
$ skipper deploy -s blue-green --auto-rollback --format json
```

### `skipper rollback`
Executes (or dry-runs) a live Kubernetes deployment rollback (`kubectl rollout undo`) and records the change and causal event in operational memory.

**Flags:**
- `--revision <rev>` — Target revision number or `previous` (default: `previous`)
- `--reason <text>` — Reason for rollback recorded in operational memory (default: `manual rollback via CLI`)
- `--context <name>` — Optional kubeconfig context name
- `--dry-run` — Preview the resolved `kubectl rollout undo` command without executing (default: `false`)
- Supports global `--format json`, `-n`/`--namespace`, and `-c`/`--config`

```bash
# Dry-run preview of rollback to revision 3
$ skipper rollback payment-api -n prod --revision 3 --reason "elevated 5xx rate" --dry-run

# Execute rollback and emit structured JSON
$ skipper rollback payment-api -n prod --context gke-prod-us --format json
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
Manages ephemeral local development Kubernetes clusters using Kind. All subcommands (`init`, `status`, `destroy`) support global `--format json`.

**Flags:**
- `--name <string>` — Local cluster name (default: `skipper-dev`)

```bash
$ skipper local init --name my-dev-cluster
$ skipper local status --format json
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
Executes the deterministic 8-detector rule matrix against live container logs and operational memory context (detects OOM kills, CrashLoopBackOff, probe failures, network drops, etc.). Supports `--format json` (strict JSON array on stdout with zero banner output).
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
- Supports global `-r`/`--runtime`, `-n`/`--namespace`, and `--format json` (with `--ephemeral`, routes subprocess output to stderr and emits pure JSON on stdout)

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
Replays chronological mutations, build events, rollouts, and debug sessions from the immutable operational memory ledger. Supports `--format json` (emits a JSON array of causal events).

**Flags:**
- `-w`, `--window <duration>` — Retrospective lookback window, e.g. `15m`, `1h`, `24h` (default: `30m`)

```bash
$ skipper time-travel payment-api --window 1h
$ skipper time-travel payment-api --window 1h --format json
```

### `skipper scan`
Runs pre-deployment vulnerability and misconfiguration audits on container images using Docker Scout, Trivy, or static baseline fallback. Supports `--format json` (routes scanner progress to stderr and emits the structured scan report JSON on stdout).
```bash
$ skipper scan payment-api:latest
$ skipper scan payment-api:latest --format json
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
Compares requested CPU/memory against actual P95 utilization from live Prometheus metrics, `kubectl top pod`, or an industry benchmark baseline to calculate monthly cloud cost savings. Supports `--format json`.

**Flags:**
- `--prometheus-url <url>` — Prometheus HTTP endpoint for live P95 CPU/memory queries
- `--live` — Query live pod metrics via `kubectl top pod`
- Uses global `-n`/`--namespace` and `-c`/`--config`

```bash
$ skipper rightsize --format json
$ skipper rightsize --prometheus-url http://prometheus.monitoring:9090 -n prod
$ skipper rightsize --live -n prod
```

---

## 5. AI Agent & MCP Integrations

### Global `--format json` Flag
Every structured CLI command (`doctor`, `init`, `build`, `up`, `down`, `status`, `deploy`, `rollback`, `manifest`, `scan`, `sbom`, `sign`, `policy-check`, `diagnose`, `rightsize`, `debug --ephemeral`, `time-travel`, `local`, `secret`, `import compose`, `observe`, `gitops export`, `context`, `explain`) supports strict, machine-parsable JSON output on stdout (routing any progress banners to stderr) tailored for LLM tool calling and CI automation:
```bash
$ skipper doctor --format json
$ skipper build --format json
$ skipper up --compose docker-compose.yml -f json
$ skipper status -f json
$ skipper diagnose payment-api -f json
$ skipper policy-check -f json
$ skipper rightsize -f json
$ skipper sbom --sbom-format cyclonedx -f json
$ skipper observe payment-api -f json
$ skipper gitops export --engine argocd -f json
$ skipper context drift --desired desired.yaml --live live.yaml -f json
$ skipper rollback payment-api --dry-run -f json
```

### `skipper mcp`
Launches an official Model Context Protocol (MCP) server over standard I/O (`stdio`), allowing AI agents (Claude Desktop, Cursor, Antigravity, Windsurf) to invoke **10 built-in Skipper operational tools** with panic recovery, 256 KiB output bounding, and strict stdout JSON-RPC 2.0 framing:

| # | Tool Name | Parameters | Description |
| :--- | :--- | :--- | :--- |
| 1 | `skipper_doctor` | *(none)* | Audits Docker daemon, `kubectl`, Kind CLI, and port conflicts |
| 2 | `skipper_init` | `name`, `port` | Inspects the project and generates a hardened `skipper.yaml` |
| 3 | `skipper_policy_check` | `configPath` | Validates `skipper.yaml` against compliance and security policies |
| 4 | `skipper_manifest` | `configPath`, `namespace` | Compiles `skipper.yaml` into hardened Kubernetes YAML manifests |
| 5 | `skipper_diagnose` | `workload`*, `logs`* | Runs the 8-detector deterministic matrix on workload logs |
| 6 | `skipper_rightsize` | `configPath`, `namespace`, `prometheusUrl`, `useKubectlTop` | Analyzes CPU/memory allocations vs live/baseline P95 usage for FinOps savings |
| 7 | `skipper_rollback` | `workload`*, `revision`, `reason`, `namespace`, `context`, `dryRun` | Executes or dry-runs `kubectl rollout undo` and records the mutation in operational memory |
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
