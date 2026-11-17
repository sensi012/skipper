# RFC 002: Operational Memory Core Data Models & Causal Event Ledger

- **Status**: Draft / Proposed
- **Author**: Isaiah Adepegba
- **Created**: 2026-11-11
- **Target Component**: Telemetry Collector, Operational Memory Daemon, Diagnostic Engine

---

## 1. Executive Summary

A critical root cause behind prolonged incident response times in modern container clusters is the **Stateless Debugging Dilemma**:
- When a pod crashes or is terminated by the Kubernetes control plane, historical debug context, ephemeral container logs, process states, and causal dependencies evaporate.
- Metrics (Prometheus) and logs (Loki/Elasticsearch) track *what* occurred in silos, but lack the direct graph linkage to *why* it occurred (e.g., who changed which config, what image was promoted, or which network policy dropped a packet).

RFC 002 specifies the domain models for **Operational Memory**:
1. `ChangeRecord`: Immutable ledger of every mutation across source code, images, configurations, and environment variables.
2. `DebugSession`: Durable capture of ephemeral interactive troubleshooting sessions.
3. `CausalGraph` (`CausalNode` & `CausalEdge`): A directed acyclic graph (DAG) correlating mutations, telemetry events, and failure symptoms into an auditable causal chain.

---

## 2. Domain Data Models

### 2.1 `ChangeRecord` (Lineage Entity)
Captures any mutation that alters cluster or workload runtime state.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["id", "timestamp", "actor", "targetWorkload", "changeType", "diff"],
  "properties": {
    "id": { "type": "string", "format": "uuid" },
    "timestamp": { "type": "string", "format": "date-time" },
    "actor": {
      "type": "object",
      "required": ["type", "identifier"],
      "properties": {
        "type": { "type": "string", "enum": ["user", "ci-pipeline", "gitops-controller", "ai-agent"] },
        "identifier": { "type": "string" },
        "ipAddress": { "type": "string" }
      }
    },
    "targetWorkload": {
      "type": "object",
      "required": ["namespace", "name"],
      "properties": {
        "cluster": { "type": "string" },
        "namespace": { "type": "string" },
        "name": { "type": "string" },
        "kind": { "type": "string", "default": "Deployment" }
      }
    },
    "changeType": {
      "type": "string",
      "enum": ["image_rollout", "config_update", "secret_rotation", "scale_event", "policy_change", "rollback"]
    },
    "gitContext": {
      "type": "object",
      "properties": {
        "commitSha": { "type": "string" },
        "branch": { "type": "string" },
        "repoUrl": { "type": "string" },
        "author": { "type": "string" }
      }
    },
    "diff": {
      "type": "object",
      "required": ["before", "after"],
      "properties": {
        "before": { "type": "object" },
        "after": { "type": "object" }
      }
    }
  }
}
```

---

### 2.2 `DebugSession` (Durable Ephemeral Context)
Ensures interactive troubleshooting actions are permanently archived.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["sessionId", "startedAt", "operator", "targetPod", "status"],
  "properties": {
    "sessionId": { "type": "string", "format": "uuid" },
    "startedAt": { "type": "string", "format": "date-time" },
    "endedAt": { "type": "string", "format": "date-time" },
    "operator": { "type": "string" },
    "targetPod": {
      "type": "object",
      "required": ["namespace", "name", "container"],
      "properties": {
        "cluster": { "type": "string" },
        "namespace": { "type": "string" },
        "name": { "type": "string" },
        "container": { "type": "string" },
        "node": { "type": "string" }
      }
    },
    "ephemeralImageUsed": { "type": "string" },
    "commandsExecuted": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["command", "executedAt", "exitCode"],
        "properties": {
          "command": { "type": "string" },
          "executedAt": { "type": "string", "format": "date-time" },
          "exitCode": { "type": "integer" },
          "stdoutSnippet": { "type": "string" },
          "stderrSnippet": { "type": "string" }
        }
      }
    },
    "artifactsCaptured": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name": { "type": "string" },
          "type": { "type": "string", "enum": ["tcpdump_pcap", "core_dump", "heap_profile", "strace_log"] },
          "storageUri": { "type": "string" }
        }
      }
    },
    "resolutionOutcome": {
      "type": "object",
      "properties": {
        "resolved": { "type": "boolean" },
        "rootCauseSummary": { "type": "string" },
        "appliedFix": { "type": "string" }
      }
    }
  }
}
```

---

### 2.3 `CausalGraph` (Event Correlation Engine)
A graph model linking upstream mutations with downstream runtime symptoms.

- **Nodes (`CausalNode`)**:
  - `MUTATION`: A `ChangeRecord` (e.g. image upgraded from `v1.2` to `v1.3`).
  - `METRIC_ANOMALY`: Metric boundary cross (e.g. container memory breached 95%).
  - `SYSTEM_SIGNAL`: OS or Kubernetes event (e.g. Linux Kernel OOM killer invoked, pod exit code 137).
  - `LOG_ANOMALY`: Panic log or recurring 5xx error surge.
  - `EBPF_NETWORK_DROP`: Cilium dropped packet due to egress policy rule #4.

- **Edges (`CausalEdge`)**:
  - Connects two nodes with attributes:
    - `relation`: `TRIGGERED`, `PRECEDED_BY`, `CORRELATED_WITH`, `RESOLVED_BY`.
    - `confidence`: $[0.0, 1.0]$ based on temporal proximity and topological dependency.
    - `timeDeltaMs`: Delay between parent and child node.

---

## 3. Query Capabilities

With this schema in place, the operational memory enables queries that are currently impossible with standard Kubernetes tools:

1. **Root-Cause Backtrace**:
   ```sql
   FIND MUTATIONS THAT PRECEDED Symptom(OOMKilled)
   WITHIN 30 MINUTES ON Workload("payment-gateway")
   ```
2. **Historical Knowledge Matching**:
   ```sql
   MATCH INCIDENTS WHERE Symptom = CrashLoopBackOff(ExitCode=139)
   RETURN AppliedFix, ResolutionAuthor, ConfidenceScore
   ```
