# RFC 001: Skipper Application Intent Specification (`skipper.yaml`) & Docker Compose Transpilation

- **Status**: Draft / Proposed
- **Author**: Isaiah Adepegba
- **Created**: 2026-11-11
- **Target Component**: Core Engine, CLI Parser, Synthesis Pipeline

---

## 1. Executive Summary

Existing container tooling forces developers to decide early between two flawed extremes:
1. **Low-fidelity, local-only developer abstractions** (e.g., standard Docker Compose), which lack security contexts, health probe contracts, resource boundaries, and multi-tenancy requirements needed for production.
2. **High-fidelity, fragmented production specifications** (e.g., raw Kubernetes Deployments, Services, Ingress, PodDisruptionBudgets, NetworkPolicies), which present an overwhelming cognitive barrier to application developers.

RFC 001 defines the **`AppSpec` (`skipper.yaml`)**: an application-intent-centric specification that is clean and intuitive enough for daily local development, yet complete enough to deterministically compile into production-grade, hardened Kubernetes manifests and OCI images without human intervention.

Additionally, this RFC defines the bidirectional mapping semantics between standard Docker Compose files (`docker-compose.yaml` / `compose.yaml`) and `skipper.yaml`.

---

## 2. Design Goals & Invariants

1. **Cognitive Ergonomics**: An application developer should configure a fully hardened microservice in under 30 lines of declarative YAML.
2. **Zero Inferred Blind Spots**: If an application omits operational properties (e.g., memory limits, readiness probes, security contexts), the platform synthesizes battle-tested, secure defaults rather than leaving them unmanaged.
3. **Lossless Transpilation**: Existing Docker Compose projects must import cleanly with zero required modifications for local runs, while generating warnings and automated upgrades for production requirements.
4. **Strict Schema Validation**: The spec is governed by JSON Schema (Draft 2020-12) with zero-tolerance validation in the CLI before any build or deploy phase is scheduled.

---

## 3. The `skipper.yaml` Specification

### 3.1 Minimal Example
```yaml
version: "v1alpha1"
name: "payment-gateway"
type: "service" # service | worker | job | cronjob

build:
  context: "."
  # CNB auto-detects Node.js/Go/Python/Java, or uses Dockerfile if present

endpoints:
  - name: "http"
    port: 8080
    public: true
    path: "/payments"

env:
  - name: "LOG_LEVEL"
    value: "info"
  - name: "DATABASE_URL"
    fromSecret: "rds-credentials.connstring"
```

### 3.2 Complete Enterprise Specification
```yaml
version: "v1alpha1"
name: "payment-gateway"
team: "fintech-core"
environment: "production"
type: "service"

build:
  context: "."
  builder: "paketobuildpacks/builder:base"
  dockerfile: "" # Optional fallback override
  args:
    NODE_ENV: "production"

runtime:
  replicas:
    min: 3
    max: 10
    targetCPUUtilization: 75
  resources:
    cpu:
      request: "250m"
      limit: "1000m"
    memory:
      request: "256Mi"
      limit: "512Mi"

endpoints:
  - name: "http"
    port: 8080
    protocol: "TCP"
    public: true
    domain: "payments.internal.company.net"
    path: "/"
    rateLimit:
      requestsPerSecond: 500
    cors:
      allowOrigins: ["https://app.company.net"]

health:
  readiness:
    path: "/healthz/ready"
    port: 8080
    initialDelaySeconds: 5
    periodSeconds: 10
  liveness:
    path: "/healthz/live"
    port: 8080
    periodSeconds: 15
  startup:
    path: "/healthz/startup"
    port: 8080
    failureThreshold: 30

security:
  runAsNonRoot: true
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop: ["ALL"]

dependencies:
  services:
    - name: "account-service"
      namespace: "fintech-core"
      required: true
    - name: "notification-service"
      namespace: "platform-messaging"
      required: false
  storage:
    - name: "transaction-cache"
      mountPath: "/data/cache"
      size: "10Gi"
      accessMode: "ReadWriteOnce"
      persistence: "ephemeral" # ephemeral (emptyDir) | persistent (PVC)

env:
  - name: "ENVIRONMENT"
    value: "production"
  - name: "STRIPE_API_KEY"
    fromSecret: "stripe-keys.secret_key"
  - name: "DB_POOL_SIZE"
    value: "20"
```

---

## 4. Docker Compose Transpilation Matrix

When importing an existing `docker-compose.yaml` via `skipper import compose.yaml`:

| Docker Compose Directive | `skipper.yaml` Equivalent | Production Kubernetes Output | Synthesis Action / Default |
| :--- | :--- | :--- | :--- |
| `image: repo:tag` | `image: repo:tag` | `image: repo:tag` | Direct pass-through |
| `build: .` | `build.context: .` | BuildKit / CNB built OCI image | Automatically tagged with Git SHA |
| `ports: ["8080:80"]` | `endpoints[0].port: 80` | `Service` (ClusterIP) + `Ingress` | Generates TLS ingress if public |
| `environment:` | `env:` | `ConfigMap` / inline env | Plain strings map to ConfigMap |
| `secrets:` | `env[].fromSecret:` | `Secret` / ExternalSecrets / SOPS | Prompts for secret vault mapping |
| `volumes: ["./data:/app"]` | `dependencies.storage:` | Local: HostPath; Prod: `PersistentVolumeClaim` | Converts host bindings to PVCs |
| `restart: always` | `runtime.restartPolicy` | `Deployment` controller restarts | Managed via Kubernetes controller |
| *(Missing limits)* | Synthesized defaults | `resources.limits` & `requests` | Auto-assigns baseline 250m/256Mi |
| *(Missing probes)* | Synthesized health | `readinessProbe` & `livenessProbe` | Auto-probes detected HTTP port |
| *(Missing security)* | Synthesized security | `securityContext` | Auto-enforces non-root & drop ALL |

---

## 5. Extensibility & Escape Hatches

Developers can inject arbitrary Kubernetes properties without invalidating `skipper.yaml`:

```yaml
# skipper.yaml
name: "special-analytics"
...
patches:
  kubernetes:
    spec:
      template:
        spec:
          tolerations:
            - key: "nvidia.com/gpu"
              operator: "Exists"
              effect: "NoSchedule"
```

The synthesizer merges `patches.kubernetes` using JSON 6902 or Strategic Merge Patch into the generated manifest.

---

## 6. Implementation Checklist

- [x] Schema specification written
- [ ] Formal JSON Schema created (`schemas/app.schema.json`)
- [ ] Transpiler unit tests with standard Compose fixtures
- [ ] Manifest synthesis engine integrating security defaults
