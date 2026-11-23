#!/usr/bin/env bash
# Automated user-level verification suite:
#   1. Clean-machine Linux installs (bare curl|bash remote release + non-root source build)
#   2. Throwaway kind Kubernetes lifecycle (manifest, up, status, upgrade+canary deploy, rollback, down)
#   3. Failure ergonomics (Docker unreachable, port collision, SIGINT mid-build)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SKIPPER_BIN="${SKIPPER_BIN:-${REPO_ROOT}/bin/skipper}"
SKIPPER_REPO="${SKIPPER_REPO:-sensi012/skipper}"
SKIPPER_VERSION="${SKIPPER_VERSION:-}"
RUN_REMOTE_INSTALL="${RUN_REMOTE_INSTALL:-true}"

OUT_DIR="${REPO_ROOT}/realworld-results/user-checks"
mkdir -p "${OUT_DIR}"
SUMMARY_FILE="${OUT_DIR}/summary.md"

if [ ! -x "${SKIPPER_BIN}" ]; then
  echo "Error: Skipper binary not executable at ${SKIPPER_BIN}" >&2
  exit 2
fi
SKIPPER_ABS="$(cd "$(dirname "${SKIPPER_BIN}")" && pwd)/$(basename "${SKIPPER_BIN}")"

PASS_COUNT=0
TOTAL_COUNT=0
ROWS=()

TMP_WORK="$(mktemp -d)"
KIND_CLUSTER="skipper-e2e"

cleanup_root() {
  rm -rf "${TMP_WORK}"
}
trap cleanup_root EXIT

run_isolated() {
  local fn="$1"
  local log_file="$2"
  local rc=0
  set +e
  (
    set -euo pipefail
    "${fn}"
  ) >"${log_file}" 2>&1
  rc=$?
  set -e
  return "${rc}"
}

record_result() {
  local name="$1"
  local status="$2"
  local details="$3"
  TOTAL_COUNT=$((TOTAL_COUNT + 1))
  if [ "${status}" = "PASS" ]; then
    PASS_COUNT=$((PASS_COUNT + 1))
  fi
  ROWS+=("| ${name} | **${status}** | ${details} |")
  echo "==> [${name}] ${status}: ${details}"
}

check_no_panic() {
  local err_file="$1"
  if grep -qE 'panic:|goroutine [0-9]+' "${err_file}" 2>/dev/null; then
    return 1
  fi
  return 0
}

validate_json_file() {
  local json_file="$1"
  if [ ! -s "${json_file}" ]; then
    return 1
  fi
  python3 -m json.tool "${json_file}" >/dev/null 2>&1
}

# -----------------------------------------------------------------------------
# Check 1a: Clean-machine Linux install via curl | bash (no Go, no repo)
# -----------------------------------------------------------------------------
if [ "${RUN_REMOTE_INSTALL}" = "true" ]; then
  C1A_LOG="${OUT_DIR}/check-1a-remote-install.log"
  if docker run --rm \
      -e "SKIPPER_REPO=${SKIPPER_REPO}" \
      -e "SKIPPER_VERSION=${SKIPPER_VERSION}" \
      ubuntu:24.04 bash -c '
        set -euo pipefail
        apt-get update -qq >/dev/null
        apt-get install -y -qq curl ca-certificates >/dev/null
        curl -fsSL "https://raw.githubusercontent.com/'"${SKIPPER_REPO}"'/main/scripts/install.sh" | bash
        skipper --version
        set +e
        doc_out="$(skipper doctor --format json 2>/dev/null)"
        set -e
        echo "$doc_out" | head -n 5
        echo "$doc_out" | grep -q "\"checks\""
      ' >"${C1A_LOG}" 2>&1; then
    ver_line="$(grep -m1 '^skipper version' "${C1A_LOG}" || echo 'installed')"
    record_result "1a: Clean Ubuntu curl|bash install" "PASS" "${ver_line}"
  else
    tail -n 20 "${C1A_LOG}" || true
    record_result "1a: Clean Ubuntu curl|bash install" "FAIL" "See ${C1A_LOG}"
  fi
fi

# -----------------------------------------------------------------------------
# Check 1b: Clean Linux git pull / source build as non-root user with sudo
# -----------------------------------------------------------------------------
C1B_LOG="${OUT_DIR}/check-1b-source-install.log"
GO_VER="$(awk '/^go / {print $2; exit}' "${REPO_ROOT}/go.mod" | cut -d. -f1-2)"
GO_IMAGE="golang:${GO_VER:-1.25}"
if docker run --rm \
    -v "${REPO_ROOT}:/src-ro:ro" \
    "${GO_IMAGE}" bash -c '
      set -euo pipefail
      apt-get update -qq >/dev/null
      apt-get install -y -qq sudo >/dev/null
      useradd -m -s /bin/bash tester
      echo "tester ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/tester
      mkdir -p /tmp/skipper-src
      cp -a /src-ro/. /tmp/skipper-src/
      rm -rf /tmp/skipper-src/bin /tmp/skipper-src/realworld-results
      chown -R tester:tester /tmp/skipper-src
      su - tester -c "export PATH=\"\$PATH:/usr/local/go/bin\" && cd /tmp/skipper-src && ./scripts/install.sh && skipper --version"
    ' >"${C1B_LOG}" 2>&1; then
  ver_line="$(grep -m1 '^skipper version' "${C1B_LOG}" || echo 'installed')"
  record_result "1b: Non-root source install.sh" "PASS" "${ver_line}"
else
  tail -n 20 "${C1B_LOG}" || true
  record_result "1b: Non-root source install.sh" "FAIL" "See ${C1B_LOG}"
fi

# -----------------------------------------------------------------------------
# Check 2: Throwaway kind Kubernetes deploy -> upgrade -> rollback -> down
# -----------------------------------------------------------------------------
C2_LOG="${OUT_DIR}/check-2-k8s-kind.log"
run_k8s_lifecycle() {
  local bin_dir="${TMP_WORK}/k8s-bin"
  mkdir -p "${bin_dir}"
  export PATH="${bin_dir}:${PATH}"

  trap 'kind delete cluster --name "${KIND_CLUSTER}" >/dev/null 2>&1 || true' EXIT

  if ! command -v kubectl >/dev/null 2>&1; then
    local kver
    kver="$(curl -fsSL https://dl.k8s.io/release/stable.txt)"
    curl -fsSL "https://dl.k8s.io/release/${kver}/bin/linux/amd64/kubectl" -o "${bin_dir}/kubectl"
    chmod +x "${bin_dir}/kubectl"
  fi
  if ! command -v kind >/dev/null 2>&1; then
    curl -fsSL "https://kind.sigs.k8s.io/dl/v0.27.0/kind-linux-amd64" -o "${bin_dir}/kind"
    chmod +x "${bin_dir}/kind"
  fi

  kind delete cluster --name "${KIND_CLUSTER}" >/dev/null 2>&1 || true
  kind create cluster --name "${KIND_CLUSTER}" --wait 120s

  local k8s_dir="${TMP_WORK}/k8s-app"
  mkdir -p "${k8s_dir}"
  cat >"${k8s_dir}/skipper.yaml" <<'EOF'
version: v1alpha1
name: k8s-smoke
team: platform
environment: staging
type: service
image: nginxinc/nginx-unprivileged:1.27-alpine
endpoints:
  - name: http
    port: 8080
    protocol: TCP
    public: true
security:
  runAsNonRoot: true
runtime:
  replicas:
    min: 1
    max: 2
  resources:
    cpu:
      request: 50m
      limit: 200m
    memory:
      request: 64Mi
      limit: 128Mi
EOF

  cd "${k8s_dir}"
  echo "--- 1. manifest ---"
  "${SKIPPER_ABS}" manifest --format json >"${OUT_DIR}/k8s-manifest.json"
  validate_json_file "${OUT_DIR}/k8s-manifest.json"
  grep -q "Deployment" "${OUT_DIR}/k8s-manifest.json"

  echo "--- 2. up v1 (1.27-alpine) ---"
  "${SKIPPER_ABS}" up -r kubernetes -n default --format json >"${OUT_DIR}/k8s-up-v1.json"
  validate_json_file "${OUT_DIR}/k8s-up-v1.json"
  kubectl rollout status deployment/k8s-smoke -n default --timeout=120s

  echo "--- 3. status v1 ---"
  "${SKIPPER_ABS}" status -r kubernetes k8s-smoke -n default --format json >"${OUT_DIR}/k8s-status-v1.json"
  validate_json_file "${OUT_DIR}/k8s-status-v1.json"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["state"]=="Running" and d["available"]>=1, d' "${OUT_DIR}/k8s-status-v1.json"

  echo "--- 4. upgrade v2 (1.26-alpine) + progressive canary deploy ---"
  "${SKIPPER_ABS}" up -r kubernetes -n default -i nginxinc/nginx-unprivileged:1.26-alpine --format json >"${OUT_DIR}/k8s-up-v2.json"
  validate_json_file "${OUT_DIR}/k8s-up-v2.json"
  kubectl rollout status deployment/k8s-smoke -n default --timeout=120s
  "${SKIPPER_ABS}" deploy -r kubernetes -n default -i nginxinc/nginx-unprivileged:1.26-alpine --format json >"${OUT_DIR}/k8s-deploy-v2.json"
  validate_json_file "${OUT_DIR}/k8s-deploy-v2.json"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert (d.get("Phase") or d.get("phase"))=="Promoted", d' "${OUT_DIR}/k8s-deploy-v2.json"
  local img_v2
  img_v2="$(kubectl get deployment k8s-smoke -n default -o jsonpath="{.spec.template.spec.containers[0].image}")"
  [ "${img_v2}" = "nginxinc/nginx-unprivileged:1.26-alpine" ]

  echo "--- 5. rollback to v1 ---"
  "${SKIPPER_ABS}" rollback k8s-smoke -n default --format json >"${OUT_DIR}/k8s-rollback.json"
  validate_json_file "${OUT_DIR}/k8s-rollback.json"
  kubectl rollout status deployment/k8s-smoke -n default --timeout=120s
  local img_rb
  img_rb="$(kubectl get deployment k8s-smoke -n default -o jsonpath="{.spec.template.spec.containers[0].image}")"
  [ "${img_rb}" = "nginxinc/nginx-unprivileged:1.27-alpine" ]

  echo "--- 6. down ---"
  "${SKIPPER_ABS}" down -r kubernetes k8s-smoke -n default --format json >"${OUT_DIR}/k8s-down.json"
  validate_json_file "${OUT_DIR}/k8s-down.json"
  local reps=""
  for _ in $(seq 1 15); do
    reps="$(kubectl get deployment k8s-smoke -n default -o jsonpath="{.spec.replicas}")"
    if [ "${reps}" = "0" ]; then
      break
    fi
    sleep 1
  done
  [ "${reps}" = "0" ]
}

if run_isolated run_k8s_lifecycle "${C2_LOG}"; then
  record_result "2: kind K8s deploy/upgrade/rollback/down" "PASS" "v1->v2(Promoted)->rollback->scaled to 0"
else
  tail -n 30 "${C2_LOG}" || true
  record_result "2: kind K8s deploy/upgrade/rollback/down" "FAIL" "See ${C2_LOG}"
fi

# -----------------------------------------------------------------------------
# Check 3a: Failure ergonomics - Docker daemon unreachable
# -----------------------------------------------------------------------------
C3A_LOG="${OUT_DIR}/check-3a-docker-down.log"
run_docker_down_check() {
  local app_dir="${TMP_WORK}/err-docker-down"
  mkdir -p "${app_dir}"
  cat >"${app_dir}/skipper.yaml" <<'EOF'
version: v1alpha1
name: err-docker-down
team: platform
environment: development
type: service
endpoints:
  - name: http
    port: 8080
    protocol: TCP
    public: true
security:
  runAsNonRoot: true
EOF
  printf 'FROM scratch\n' >"${app_dir}/Dockerfile"

  cd "${app_dir}"
  set +e
  DOCKER_HOST="tcp://127.0.0.1:1" "${SKIPPER_ABS}" doctor --format json >"${OUT_DIR}/3a-doctor.out" 2>"${OUT_DIR}/3a-doctor.err"
  local rc_doc=$?
  DOCKER_HOST="tcp://127.0.0.1:1" "${SKIPPER_ABS}" build >"${OUT_DIR}/3a-build.out" 2>"${OUT_DIR}/3a-build.err"
  local rc_bld=$?
  DOCKER_HOST="tcp://127.0.0.1:1" "${SKIPPER_ABS}" up >"${OUT_DIR}/3a-up.out" 2>"${OUT_DIR}/3a-up.err"
  local rc_up=$?
  set -e
  echo "exit codes: doctor=${rc_doc}, build=${rc_bld}, up=${rc_up}"
  [ "${rc_doc}" -ne 0 ] && [ "${rc_bld}" -ne 0 ] && [ "${rc_up}" -ne 0 ]
  check_no_panic "${OUT_DIR}/3a-doctor.err"
  check_no_panic "${OUT_DIR}/3a-build.err"
  check_no_panic "${OUT_DIR}/3a-up.err"
  [ -s "${OUT_DIR}/3a-build.err" ] && [ -s "${OUT_DIR}/3a-up.err" ]
  validate_json_file "${OUT_DIR}/3a-doctor.out"
}
if run_isolated run_docker_down_check "${C3A_LOG}"; then
  record_result "3a: Docker unreachable ergonomics" "PASS" "Non-zero exit, clean stderr/JSON, 0 panics"
else
  cat "${C3A_LOG}" || true
  record_result "3a: Docker unreachable ergonomics" "FAIL" "See ${C3A_LOG}"
fi

# -----------------------------------------------------------------------------
# Check 3b: Failure ergonomics - Host port already in use
# -----------------------------------------------------------------------------
C3B_LOG="${OUT_DIR}/check-3b-port-collision.log"
run_port_collision_check() {
  PORT_PID=""
  trap 'if [ -n "${PORT_PID:-}" ]; then kill "${PORT_PID}" >/dev/null 2>&1 || true; wait "${PORT_PID}" >/dev/null 2>&1 || true; fi; docker rm -f err-port-test >/dev/null 2>&1 || true' EXIT

  local app_dir="${TMP_WORK}/err-port"
  mkdir -p "${app_dir}"
  cat >"${app_dir}/skipper.yaml" <<'EOF'
version: v1alpha1
name: err-port-test
team: platform
environment: development
type: service
image: alpine:3.20
endpoints:
  - name: http
    port: 18999
    protocol: TCP
    public: true
security:
  runAsNonRoot: true
EOF
  docker pull -q alpine:3.20 >/dev/null
  python3 -c 'import socket,time; s=socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); s.bind(("0.0.0.0", 18999)); s.listen(1); time.sleep(60)' &
  PORT_PID=$!
  sleep 1

  cd "${app_dir}"
  set +e
  "${SKIPPER_ABS}" up >"${OUT_DIR}/3b-up.out" 2>"${OUT_DIR}/3b-up.err"
  local rc=$?
  set -e
  echo "exit=${rc}"
  cat "${OUT_DIR}/3b-up.err"
  [ "${rc}" -ne 0 ]
  check_no_panic "${OUT_DIR}/3b-up.err"
  [ -s "${OUT_DIR}/3b-up.err" ]
}
if run_isolated run_port_collision_check "${C3B_LOG}"; then
  record_result "3b: Host port collision ergonomics" "PASS" "Non-zero exit, clear bind error, 0 panics"
else
  cat "${C3B_LOG}" || true
  record_result "3b: Host port collision ergonomics" "FAIL" "See ${C3B_LOG}"
fi

# -----------------------------------------------------------------------------
# Check 3c: Failure ergonomics - SIGINT (Ctrl+C) mid-build
# -----------------------------------------------------------------------------
C3C_LOG="${OUT_DIR}/check-3c-sigint.log"
run_sigint_check() {
  local app_dir="${TMP_WORK}/err-sigint"
  mkdir -p "${app_dir}"
  cat >"${app_dir}/skipper.yaml" <<'EOF'
version: v1alpha1
name: err-sigint-test
team: platform
environment: development
type: service
security:
  runAsNonRoot: true
EOF
  printf 'FROM alpine:3.20\nRUN sleep 30\n' >"${app_dir}/Dockerfile"

  cd "${app_dir}"
  python3 - "${SKIPPER_ABS}" "${OUT_DIR}/3c-build.out" "${OUT_DIR}/3c-build.err" <<'PY'
import os, signal, subprocess, sys, time
exe, out_p, err_p = sys.argv[1], sys.argv[2], sys.argv[3]
def reset_sigint():
    signal.signal(signal.SIGINT, signal.SIG_DFL)
    os.setsid()
with open(out_p, "wb") as fo, open(err_p, "wb") as fe:
    t0 = time.monotonic()
    p = subprocess.Popen([exe, "build"], stdout=fo, stderr=fe, preexec_fn=reset_sigint)
    time.sleep(3)
    os.killpg(os.getpgid(p.pid), signal.SIGINT)
    rc = p.wait(timeout=15)
    elapsed = time.monotonic() - t0
print(f"sigint exit={rc} elapsed={elapsed:.2f}s")
assert rc != 0, f"expected non-zero exit after SIGINT, got {rc}"
assert elapsed < 20.0, f"build was not interrupted cleanly (elapsed={elapsed:.2f}s)"
PY
  check_no_panic "${OUT_DIR}/3c-build.err"
  check_no_panic "${OUT_DIR}/3c-build.out"
}
if run_isolated run_sigint_check "${C3C_LOG}"; then
  sig_line="$(grep -m1 '^sigint exit=' "${C3C_LOG}" || echo 'interrupted')"
  record_result "3c: SIGINT mid-build ergonomics" "PASS" "${sig_line}, 0 panics"
else
  cat "${C3C_LOG}" || true
  record_result "3c: SIGINT mid-build ergonomics" "FAIL" "See ${C3C_LOG}"
fi

# -----------------------------------------------------------------------------
# Write Markdown Summary
# -----------------------------------------------------------------------------
{
  echo "# Skipper User-Level Verification Summary"
  echo ""
  echo "- **Binary:** \`${SKIPPER_ABS}\`"
  echo "- **Summary:** ${PASS_COUNT}/${TOTAL_COUNT} passed"
  echo ""
  echo "| Check | Status | Details |"
  echo "| :--- | :--- | :--- |"
  for row in "${ROWS[@]}"; do
    echo "${row}"
  done
} >"${SUMMARY_FILE}"

echo ""
cat "${SUMMARY_FILE}"

if [ "${PASS_COUNT}" -ne "${TOTAL_COUNT}" ]; then
  exit 1
fi
exit 0
