# RFC 003: Dual-Mode Protocol, Storage Engine & Control Plane Architecture

- **Status**: Draft / Proposed
- **Author**: Isaiah Adepegba
- **Created**: 2026-11-11
- **Target Component**: Storage Subsystem, Daemon Engine, gRPC/REST APIs

---

## 1. Executive Summary

A core failure mode of modern developer platforms is requiring an active cloud connection or heavy server cluster even for simple local offline development. Conversely, lightweight developer tools fail to scale to multi-cluster enterprise topologies.

RFC 003 outlines the **Dual-Mode Architectural Pattern**:
1. **Local Mode (Zero-Dependency Offline Developer Loop)**:
   - Single embedded binary.
   - Embedded storage (`SQLite` via WAL mode or embedded `DuckDB`).
   - Local ephemeral container runner (Docker socket or embedded Kind/K3d).
2. **Production / Enterprise Mode (Distributed Fleet Control Plane)**:
   - High-throughput ingestion daemon (`Causal Ledger Operator`).
   - Distributed relational & time-series storage (`PostgreSQL` + `ClickHouse` / pgvector).
   - Multi-cluster gRPC streaming and mTLS authentication.

---

## 2. Storage Engine Topology

```
┌────────────────────────────────────────────────────────┐
│                   Developer CLI                        │
└──────────────────────────┬─────────────────────────────┘
                           │
             ┌─────────────┴─────────────┐
             ▼                           ▼
    [ Local Mode ]              [ Production Mode ]
    Target: localhost           Target: Remote K8s Cluster
             │                           │
  ┌──────────▼───────────┐      ┌────────▼────────────────┐
  │ Embedded SQLite /    │      │  gRPC / Connect API     │
  │ DuckDB Storage       │      │  mTLS + OAuth2 Token    │
  │ File: ~/.app/db.dat  │      └────────┬────────────────┘
  └──────────────────────┘               │
                                ┌────────▼────────────────┐
                                │ Platform Ingestion Hub  │
                                └────────┬────────────────┘
                                         │
                        ┌────────────────┴───────────────┐
                        ▼                                ▼
              [ Timescale/ClickHouse ]          [ PostgreSQL / Vector ]
              • eBPF Telemetry Flows            • ChangeRecords
              • High-Volume Event Stream        • DebugSession Archives
              • Metric Time-Series              • Graph Adjacency List
```

---

## 3. Control Plane Protocol Contracts (gRPC / Protocol Buffers)

```protobuf
syntax = "proto3";

package platform.v1alpha1;

service OperationalMemoryService {
  // Lineage & Mutations
  rpc RecordChange (ChangeRecordRequest) returns (ChangeRecordResponse);
  rpc GetChangeHistory (ChangeHistoryRequest) returns (ChangeHistoryResponse);

  // Debug Session Stream
  rpc StartDebugSession (StartDebugSessionRequest) returns (DebugSessionHandle);
  rpc StreamDebugLogs (stream DebugLogPacket) returns (DebugSessionStatus);
  rpc CloseDebugSession (CloseDebugSessionRequest) returns (DebugSessionSummary);

  // Time-Travel & Causal Analysis
  rpc QueryCausalTimeline (CausalTimelineRequest) returns (CausalTimelineResponse);
  rpc CorrelateIncident (IncidentCorrelationRequest) returns (DiagnosticReport);
}

service SynthesisEngineService {
  rpc TranspileCompose (TranspileComposeRequest) returns (TranspileComposeResponse);
  rpc SynthesizeManifests (SynthesizeManifestsRequest) returns (SynthesizeManifestsResponse);
  rpc ValidateAppSpec (ValidateAppSpecRequest) returns (ValidationResult);
}
```

---

## 4. Architectural Invariants for Enterprise Environments

1. **Air-Gapped Operation**:
   - The CLI must be fully functional offline for local builds and local mock deploys with embedded SQLite.
2. **Telemetry Privacy & Secret Redaction**:
   - The ingestion daemon must strip and hash environment variables matching secret patterns (`*KEY*`, `*TOKEN*`, `*SECRET*`, `*PASSWORD*`) before persisting to the causal log.
3. **Data Retention & Compaction Policies**:
   - Operational memory events follow Tiered Storage:
     - Raw eBPF flows: 7 days retention.
     - Correlated causal graph & `ChangeRecord` metadata: 365 days retention.
     - Resolved incident diagnostic playbooks: Permanent.
