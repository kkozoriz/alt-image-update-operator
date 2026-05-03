#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBECONFIG_PATH="${KUBECONFIG:-}"
EXPECTED_KUBECONFIG="${HOME}/.kube-remote/minikube-config"
NAMESPACE="${E2E_NAMESPACE:-alt-image-update-demo}"
ALWAYS_POLICY_NAME="${E2E_ALWAYS_POLICY_NAME:-demo-app-always}"
ALT_APT_POLICY_NAME="${E2E_ALT_APT_POLICY_NAME:-demo-app-alt-apt-simulation}"
DEPLOYMENT_NAME="${E2E_DEPLOYMENT_NAME:-demo-app}"
CONTAINER_NAME="${E2E_CONTAINER_NAME:-app}"
REGISTRY_NAMESPACE="${E2E_REGISTRY_NAMESPACE:-kube-system}"
REGISTRY_SERVICE="${E2E_REGISTRY_SERVICE:-registry}"
REGISTRY_FORWARD_PORT="${E2E_REGISTRY_FORWARD_PORT:-5005}"
BUILD_TIMEOUT="${E2E_BUILD_TIMEOUT:-900s}"
ROLLOUT_TIMEOUT="${E2E_ROLLOUT_TIMEOUT:-300s}"
STATUS_TIMEOUT_SECONDS="${E2E_STATUS_TIMEOUT_SECONDS:-300}"
POLL_SECONDS="${E2E_POLL_SECONDS:-5}"

MANAGER_PID=""
REGISTRY_FORWARD_PID=""
WORK_DIR=""

log() {
  printf '[e2e] %s\n' "$*"
}

fail() {
  printf '[e2e] ERROR: %s\n' "$*" >&2
  collect_debug || true
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

kubectl_cmd() {
  local attempt output rc
  for attempt in 1 2 3 4 5; do
    output="$(kubectl_raw "$@" 2>&1)" && {
      printf '%s' "$output"
      return 0
    }
    rc=$?
    if [[ "$output" == *"Unable to connect to the server"* || "$output" == *"operation not permitted"* || "$output" == *"connection refused"* ]]; then
      sleep "$attempt"
      continue
    fi
    printf '%s\n' "$output" >&2
    return "$rc"
  done
  printf '%s\n' "$output" >&2
  return "$rc"
}

kubectl_raw() {
  kubectl --kubeconfig "$KUBECONFIG_PATH" "$@"
}

cleanup() {
  local status=$?
  if [[ -n "$REGISTRY_FORWARD_PID" ]] && kill -0 "$REGISTRY_FORWARD_PID" >/dev/null 2>&1; then
    kill "$REGISTRY_FORWARD_PID" >/dev/null 2>&1 || true
    wait "$REGISTRY_FORWARD_PID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$MANAGER_PID" ]] && kill -0 "$MANAGER_PID" >/dev/null 2>&1; then
    kill "$MANAGER_PID" >/dev/null 2>&1 || true
    wait "$MANAGER_PID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$WORK_DIR" && -d "$WORK_DIR" ]]; then
    rm -rf "$WORK_DIR"
  fi
  exit "$status"
}
trap cleanup EXIT

collect_debug() {
  log "collecting debug data"
  if [[ -n "$WORK_DIR" && -f "$WORK_DIR/manager.log" ]]; then
    log "controller manager log tail"
    tail -80 "$WORK_DIR/manager.log" || true
  fi
  kubectl_cmd -n "$NAMESPACE" get altimageupdatepolicies -o wide || true
  kubectl_cmd -n "$NAMESPACE" get altimageupdatepolicy "$ALWAYS_POLICY_NAME" -o yaml || true
  kubectl_cmd -n "$NAMESPACE" get altimageupdatepolicy "$ALT_APT_POLICY_NAME" -o yaml || true
  kubectl_cmd -n "$NAMESPACE" get jobs || true
  kubectl_cmd -n "$NAMESPACE" get pods || true
  kubectl_cmd -n "$NAMESPACE" get events --sort-by=.metadata.creationTimestamp || true
  kubectl_cmd -n "$NAMESPACE" describe deployment "$DEPLOYMENT_NAME" || true
}

require_allowed_kubeconfig() {
  [[ -n "$KUBECONFIG_PATH" ]] || fail "KUBECONFIG must be set to $EXPECTED_KUBECONFIG"
  [[ -f "$KUBECONFIG_PATH" ]] || fail "KUBECONFIG file does not exist: $KUBECONFIG_PATH"

  local actual expected
  actual="$(cd "$(dirname "$KUBECONFIG_PATH")" && pwd -P)/$(basename "$KUBECONFIG_PATH")"
  expected="$(cd "$(dirname "$EXPECTED_KUBECONFIG")" && pwd -P)/$(basename "$EXPECTED_KUBECONFIG")"
  [[ "$actual" == "$expected" ]] || fail "refusing to use kubeconfig $actual; expected $expected"

  local context server node_ready node_ip
  context="$(kubectl_cmd config current-context)"
  server="$(kubectl_cmd config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
  [[ "$context" == "minikube" ]] || fail "refusing context $context; expected minikube"
  [[ "$server" == "https://127.0.0.1:16443" ]] || fail "refusing API server $server; expected https://127.0.0.1:16443"

  node_ready="$(kubectl_cmd get node minikube -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')"
  [[ "$node_ready" == "True" ]] || fail "remote minikube node is not Ready"

  node_ip="$(kubectl_cmd get node minikube -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')"
  [[ "$node_ip" == "192.168.49.2" ]] || fail "refusing node InternalIP $node_ip; expected remote minikube 192.168.49.2"
}

wait_for_manager() {
  local deadline=$((SECONDS + 120))
  while (( SECONDS < deadline )); do
    if ! kill -0 "$MANAGER_PID" >/dev/null 2>&1; then
      fail "controller manager exited before becoming ready"
    fi
    if grep -q 'Starting manager' "$WORK_DIR/manager.log" 2>/dev/null; then
      log "controller manager started"
      return
    fi
    sleep 1
  done
  fail "timed out waiting for controller manager startup"
}

build_manager() {
  log "building local controller manager"
  (
    cd "$ROOT_DIR"
    GOCACHE="${GOCACHE:-$ROOT_DIR/.gocache/build}" go build -o "$WORK_DIR/manager" ./cmd/main.go
  )
}

wait_for_jsonpath_value() {
  local description="$1"
  local expected="$2"
  shift 2
  local deadline=$((SECONDS + STATUS_TIMEOUT_SECONDS))
  local value=""
  while (( SECONDS < deadline )); do
    value="$(kubectl_cmd "$@" 2>/dev/null || true)"
    if [[ "$value" == "$expected" ]]; then
      log "$description is $expected"
      return
    fi
    sleep "$POLL_SECONDS"
  done
  fail "timed out waiting for $description=$expected; last value: ${value:-<empty>}"
}

wait_for_job_name() {
  local policy_name="$1"
  local job_type="$2"
  local description="$3"
  local deadline=$((SECONDS + STATUS_TIMEOUT_SECONDS))
  local job_name=""
  while (( SECONDS < deadline )); do
    job_name="$(kubectl_cmd -n "$NAMESPACE" get jobs \
      -l "security.altlinux.org/job-type=${job_type},security.altlinux.org/policy-name=${policy_name}" \
      -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
    if [[ -n "$job_name" ]]; then
      printf '%s\n' "$job_name"
      return
    fi
    sleep "$POLL_SECONDS"
  done
  fail "timed out waiting for ${description} creation"
}

wait_for_build_job_name() {
  local policy_name="$1"
  wait_for_job_name "$policy_name" "build" "Build Job for policy ${policy_name}"
}

wait_for_check_job_name() {
  local policy_name="$1"
  wait_for_job_name "$policy_name" "check" "Check Job for policy ${policy_name}"
}

wait_for_check_result_reason() {
  local policy_name="$1"
  local deadline=$((SECONDS + STATUS_TIMEOUT_SECONDS))
  local reason phase=""
  while (( SECONDS < deadline )); do
    reason="$(kubectl_cmd -n "$NAMESPACE" get altimageupdatepolicy "$policy_name" \
      -o jsonpath='{.status.conditions[?(@.type=="CheckCompleted")].reason}' 2>/dev/null || true)"
    phase="$(kubectl_cmd -n "$NAMESPACE" get altimageupdatepolicy "$policy_name" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    if [[ "$reason" == "NoUpdates" || "$reason" == "UpdatesAvailable" ]]; then
      printf '%s\n' "$reason"
      return
    fi
    if [[ "$phase" == "Failed" ]]; then
      fail "AltAptSimulation policy ${policy_name} failed before a successful check result"
    fi
    sleep "$POLL_SECONDS"
  done
  fail "timed out waiting for CheckCompleted reason on policy ${policy_name}; last phase: ${phase:-<empty>}"
}

duration_seconds() {
  local value="$1"
  if [[ "$value" =~ ^([0-9]+)s$ ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return
  fi
  if [[ "$value" =~ ^[0-9]+$ ]]; then
    printf '%s\n' "$value"
    return
  fi
  printf '%s\n' "900"
}

wait_for_job_complete() {
  local job_name="$1"
  local description="$2"
  local timeout_seconds complete failed
  timeout_seconds="$(duration_seconds "$BUILD_TIMEOUT")"
  local deadline=$((SECONDS + timeout_seconds))

  while (( SECONDS < deadline )); do
    complete="$(kubectl_cmd -n "$NAMESPACE" get job "$job_name" -o jsonpath='{.status.conditions[?(@.type=="Complete")].status}' 2>/dev/null || true)"
    if [[ "$complete" == "True" ]]; then
      log "${description} ${job_name} completed"
      return
    fi

    failed="$(kubectl_cmd -n "$NAMESPACE" get job "$job_name" -o jsonpath='{.status.conditions[?(@.type=="Failed")].status}' 2>/dev/null || true)"
    if [[ "$failed" == "True" ]]; then
      kubectl_cmd -n "$NAMESPACE" logs "job/${job_name}" --tail=120 || true
      fail "${description} ${job_name} failed"
    fi

    sleep "$POLL_SECONDS"
  done

  fail "timed out waiting for ${description} ${job_name} to complete"
}

stop_registry_port_forward() {
  if [[ -n "$REGISTRY_FORWARD_PID" ]] && kill -0 "$REGISTRY_FORWARD_PID" >/dev/null 2>&1; then
    kill "$REGISTRY_FORWARD_PID" >/dev/null 2>&1 || true
    wait "$REGISTRY_FORWARD_PID" >/dev/null 2>&1 || true
  fi
  REGISTRY_FORWARD_PID=""
}

start_registry_port_forward() {
  kubectl_cmd -n "$REGISTRY_NAMESPACE" get svc "$REGISTRY_SERVICE" >/dev/null
  kubectl_cmd -n "$REGISTRY_NAMESPACE" rollout status daemonset/registry-proxy --timeout=120s >/dev/null
  wait_for_registry_endpoint

  kubectl --kubeconfig "$KUBECONFIG_PATH" -n "$REGISTRY_NAMESPACE" port-forward --address 127.0.0.1 "svc/${REGISTRY_SERVICE}" \
    "${REGISTRY_FORWARD_PORT}:80" >"$WORK_DIR/registry-port-forward.log" 2>&1 &
  REGISTRY_FORWARD_PID=$!

  local deadline=$((SECONDS + 30))
  while (( SECONDS < deadline )); do
    if ! kill -0 "$REGISTRY_FORWARD_PID" >/dev/null 2>&1; then
      if [[ -f "$WORK_DIR/registry-port-forward.log" ]]; then
        cat "$WORK_DIR/registry-port-forward.log" >&2 || true
      fi
      fail "registry port-forward exited unexpectedly"
    fi
    if grep -q 'Forwarding from 127.0.0.1' "$WORK_DIR/registry-port-forward.log"; then
      return
    fi
    sleep 1
  done
  fail "timed out waiting for registry port-forward"
}

wait_for_registry_endpoint() {
  local deadline=$((SECONDS + 120))
  local endpoint=""
  while (( SECONDS < deadline )); do
    endpoint="$(kubectl_cmd -n "$REGISTRY_NAMESPACE" get endpoints "$REGISTRY_SERVICE" -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null || true)"
    if [[ -n "$endpoint" ]]; then
      return
    fi
    sleep 1
  done
  fail "timed out waiting for registry endpoint"
}

reset_registry_storage() {
  log "resetting minikube registry storage before next e2e scenario"
  stop_registry_port_forward
  kubectl_cmd -n "$REGISTRY_NAMESPACE" delete pod -l actual-registry=true --ignore-not-found=true --wait=false >/dev/null || true
  kubectl_cmd -n "$REGISTRY_NAMESPACE" rollout status deployment/registry --timeout=120s >/dev/null
  start_registry_port_forward
}

cleanup_policy_artifacts() {
  local policy_name="$1"
  kubectl_cmd -n "$NAMESPACE" delete jobs -l "security.altlinux.org/policy-name=${policy_name}" --ignore-not-found=true --wait=false >/dev/null || true
  kubectl_cmd -n "$NAMESPACE" delete altimageupdatepolicy "$policy_name" --ignore-not-found=true >/dev/null || true
}

image_repo_and_tag() {
  local image="$1"
  local remainder="${image#*/}"
  local tag="${remainder##*:}"
  local repo="${remainder%:*}"
  [[ "$image" == */* && "$repo" != "$remainder" && -n "$tag" ]] || fail "cannot parse built image reference: $image"
  printf '%s %s\n' "$repo" "$tag"
}

verify_registry_tag() {
  local built_image="$1"
  local repo tag body url
  read -r repo tag < <(image_repo_and_tag "$built_image")
  url="http://127.0.0.1:${REGISTRY_FORWARD_PORT}/v2/${repo}/tags/list"

  body="$(curl -fsS "$url")" || fail "registry tags endpoint failed: $url"
  printf '%s\n' "$body" | grep -F "\"${tag}\"" >/dev/null || fail "registry tags endpoint does not contain tag ${tag}: ${body}"
  log "registry contains ${repo}:${tag}"
}

render_policy() {
  local output_image="$1"
  local manual_token="remote-minikube-$(date -u +%Y%m%dT%H%M%SZ)"
  local policy_file="$WORK_DIR/policy-always.yaml"

  sed \
    -e "s#manualToken: .*#manualToken: ${manual_token}#" \
    -e "s#outputImage: .*#outputImage: ${output_image}#" \
    "$ROOT_DIR/test/e2e/manifests/policy-always.yaml" >"$policy_file"
  printf '%s\n' "$policy_file"
}

render_alt_apt_policy() {
  local output_image="$1"
  local manual_token="remote-minikube-alt-apt-$(date -u +%Y%m%dT%H%M%SZ)"
  local policy_file="$WORK_DIR/policy-alt-apt-simulation.yaml"

  sed \
    -e "s#manualToken: .*#manualToken: ${manual_token}#" \
    -e "s#outputImage: .*#outputImage: ${output_image}#" \
    "$ROOT_DIR/test/e2e/manifests/policy-alt-apt-simulation.yaml" >"$policy_file"
  printf '%s\n' "$policy_file"
}

alt_apt_log_has_updates() {
  local log_file="$1"
  local line

  grep -E '^(Inst|Remv)[[:space:]][^[:space:]]+' "$log_file" >/dev/null && return 0
  grep -Eiq '^The following packages? will be (upgraded|REMOVED):?$' "$log_file" && return 0
  grep -Eiq '^The following NEW packages? will be installed:?$' "$log_file" && return 0
  while IFS= read -r line; do
    if [[ "$line" =~ ([0-9]+)[[:space:]]upgraded,[[:space:]]([0-9]+)[[:space:]]newly[[:space:]]installed,[[:space:]]([0-9]+)[[:space:]]removed ]]; then
      if (( BASH_REMATCH[1] + BASH_REMATCH[2] + BASH_REMATCH[3] > 0 )); then
        return 0
      fi
    fi
  done <"$log_file"
  return 1
}

verify_alt_apt_check_logs() {
  local check_job="$1"
  local log_file="$2"

  kubectl_cmd -n "$NAMESPACE" logs "job/${check_job}" -c alt-apt-simulation >"$log_file"
  grep -F "RUN apt-get update" "$log_file" >/dev/null || fail "Check Job ${check_job} logs do not show apt-get update"
  grep -F "RUN apt-get -s dist-upgrade" "$log_file" >/dev/null || fail "Check Job ${check_job} logs do not show apt-get -s dist-upgrade"
  if grep -E '^E:[[:space:]]|sub-process.*returned an error code|apt-get.*failed' "$log_file" >/dev/null; then
    fail "Check Job ${check_job} logs contain apt errors"
  fi
  log "Check Job ${check_job} logs show apt-get update and apt-get -s dist-upgrade"
}

verify_successful_policy_rollout() {
  local policy_name="$1"
  local build_job="$2"
  local built_image applied_image deployment_image

  wait_for_jsonpath_value "policy ${policy_name} phase" "Succeeded" \
    -n "$NAMESPACE" get altimageupdatepolicy "$policy_name" -o jsonpath='{.status.phase}'

  built_image="$(kubectl_cmd -n "$NAMESPACE" get altimageupdatepolicy "$policy_name" -o jsonpath='{.status.lastBuiltImage}')"
  applied_image="$(kubectl_cmd -n "$NAMESPACE" get altimageupdatepolicy "$policy_name" -o jsonpath='{.status.lastAppliedImage}')"
  [[ -n "$built_image" ]] || fail "policy ${policy_name} status.lastBuiltImage is empty"
  [[ -n "$applied_image" ]] || fail "policy ${policy_name} status.lastAppliedImage is empty"
  [[ "$built_image" == "$applied_image" ]] || fail "policy ${policy_name} lastBuiltImage and lastAppliedImage differ: ${built_image} != ${applied_image}"

  verify_registry_tag "$built_image"

  deployment_image="$(kubectl_cmd -n "$NAMESPACE" get deployment "$DEPLOYMENT_NAME" \
    -o 'go-template={{range .spec.template.spec.containers}}{{if eq .name "'"$CONTAINER_NAME"'"}}{{.image}}{{end}}{{end}}')"
  [[ "$deployment_image" == "$applied_image" ]] || fail "Deployment image ${deployment_image} does not equal policy ${policy_name} status.lastAppliedImage ${applied_image}"

  kubectl_cmd -n "$NAMESPACE" rollout status "deployment/${DEPLOYMENT_NAME}" --timeout="$ROLLOUT_TIMEOUT"
  wait_for_jsonpath_value "policy ${policy_name} rollout condition" "True" \
    -n "$NAMESPACE" get altimageupdatepolicy "$policy_name" -o jsonpath='{.status.conditions[?(@.type=="RolloutCompleted")].status}'

  log "policy ${policy_name} completed: Build Job ${build_job} pushed ${built_image}, Deployment uses ${deployment_image}"
}

run_alt_apt_simulation_policy() {
  local output_image="$1"
  local policy_file check_job check_log check_reason build_job build_jobs

  policy_file="$(render_alt_apt_policy "$output_image")"
  log "applying AltAptSimulation policy ${ALT_APT_POLICY_NAME}"
  kubectl_cmd -n "$NAMESPACE" apply -f "$policy_file"

  check_job="$(wait_for_check_job_name "$ALT_APT_POLICY_NAME")"
  log "waiting for real AltAptSimulation Check Job ${check_job}"
  wait_for_job_complete "$check_job" "Check Job"

  check_log="$WORK_DIR/${check_job}.log"
  verify_alt_apt_check_logs "$check_job" "$check_log"

  check_reason="$(wait_for_check_result_reason "$ALT_APT_POLICY_NAME")"
  log "AltAptSimulation policy ${ALT_APT_POLICY_NAME} classified check logs as ${check_reason}"

  if alt_apt_log_has_updates "$check_log"; then
    [[ "$check_reason" == "UpdatesAvailable" ]] || fail "Check Job logs show updates, but policy reason is ${check_reason}"
    build_job="$(wait_for_build_job_name "$ALT_APT_POLICY_NAME")"
    log "updates detected; waiting for real Kaniko Build Job ${build_job}"
    wait_for_job_complete "$build_job" "Build Job"
    verify_successful_policy_rollout "$ALT_APT_POLICY_NAME" "$build_job"
    return
  fi

  [[ "$check_reason" == "NoUpdates" ]] || fail "Check Job logs show no updates, but policy reason is ${check_reason}"
  wait_for_jsonpath_value "policy ${ALT_APT_POLICY_NAME} phase" "UpToDate" \
    -n "$NAMESPACE" get altimageupdatepolicy "$ALT_APT_POLICY_NAME" -o jsonpath='{.status.phase}'
  build_jobs="$(kubectl_cmd -n "$NAMESPACE" get jobs \
    -l "security.altlinux.org/job-type=build,security.altlinux.org/policy-name=${ALT_APT_POLICY_NAME}" \
    -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)"
  [[ -z "$build_jobs" ]] || fail "AltAptSimulation no-updates run created unexpected Build Job(s): ${build_jobs}"
  log "no updates detected; policy ${ALT_APT_POLICY_NAME} is UpToDate and no Build Job was created for the run"
}

main() {
  need_cmd kubectl
  need_cmd go
  need_cmd curl
  require_allowed_kubeconfig

  WORK_DIR="$(mktemp -d)"
  local node_ip output_image policy_file build_job
  node_ip="$(kubectl_cmd get node minikube -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')"
  [[ -n "$node_ip" ]] || fail "remote minikube node InternalIP is empty"
  output_image="${E2E_OUTPUT_IMAGE:-localhost:5000/alt/demo-app}"

  log "using remote minikube registry image repository ${output_image}"
  start_registry_port_forward

  log "installing generated CRDs"
  kubectl_cmd apply -k "$ROOT_DIR/config/crd"

  log "recreating demo namespace ${NAMESPACE}"
  kubectl_cmd delete namespace "$NAMESPACE" --ignore-not-found=true --wait=true
  kubectl_cmd create namespace "$NAMESPACE"

  log "starting local controller manager against remote minikube"
  build_manager
  KUBECONFIG="$KUBECONFIG_PATH" "$WORK_DIR/manager" \
    --metrics-bind-address=0 \
    --health-probe-bind-address=:0 >"$WORK_DIR/manager.log" 2>&1 &
  MANAGER_PID=$!
  wait_for_manager

  log "applying demo Deployment and ConfigMap"
  kubectl_cmd -n "$NAMESPACE" apply -f "$ROOT_DIR/test/e2e/manifests/context-configmap.yaml"
  kubectl_cmd -n "$NAMESPACE" apply -f "$ROOT_DIR/test/e2e/manifests/deployment.yaml"
  kubectl_cmd -n "$NAMESPACE" rollout status "deployment/${DEPLOYMENT_NAME}" --timeout="$ROLLOUT_TIMEOUT"

  policy_file="$(render_policy "$output_image")"
  log "applying Always policy ${ALWAYS_POLICY_NAME}"
  kubectl_cmd -n "$NAMESPACE" apply -f "$policy_file"

  build_job="$(wait_for_build_job_name "$ALWAYS_POLICY_NAME")"
  log "waiting for real Kaniko Build Job ${build_job}"
  wait_for_job_complete "$build_job" "Build Job"

  verify_successful_policy_rollout "$ALWAYS_POLICY_NAME" "$build_job"
  cleanup_policy_artifacts "$ALWAYS_POLICY_NAME"
  reset_registry_storage
  run_alt_apt_simulation_policy "$output_image"

  log "DONE: Always and AltAptSimulation e2e scenarios passed in namespace ${NAMESPACE}"
}

main "$@"
