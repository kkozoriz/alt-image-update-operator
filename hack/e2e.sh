#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBECONFIG_PATH="${KUBECONFIG:-}"
EXPECTED_KUBECONFIG="${HOME}/.kube-remote/minikube-config"
NAMESPACE="${E2E_NAMESPACE:-alt-image-update-demo}"
POLICY_NAME="${E2E_POLICY_NAME:-demo-app-always}"
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
  kubectl_cmd -n "$NAMESPACE" get altimageupdatepolicy "$POLICY_NAME" -o yaml || true
  kubectl_cmd -n "$NAMESPACE" get jobs,pods,events --sort-by=.metadata.creationTimestamp || true
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

wait_for_build_job_name() {
  local deadline=$((SECONDS + STATUS_TIMEOUT_SECONDS))
  local job_name=""
  while (( SECONDS < deadline )); do
    job_name="$(kubectl_cmd -n "$NAMESPACE" get jobs \
      -l "security.altlinux.org/job-type=build,security.altlinux.org/policy-name=${POLICY_NAME}" \
      -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
    if [[ -n "$job_name" ]]; then
      printf '%s\n' "$job_name"
      return
    fi
    sleep "$POLL_SECONDS"
  done
  fail "timed out waiting for Build Job creation"
}

start_registry_port_forward() {
  kubectl_cmd -n "$REGISTRY_NAMESPACE" get svc "$REGISTRY_SERVICE" >/dev/null
  kubectl_cmd -n "$REGISTRY_NAMESPACE" rollout status daemonset/registry-proxy --timeout=120s >/dev/null

  kubectl_raw -n "$REGISTRY_NAMESPACE" port-forward --address 127.0.0.1 "svc/${REGISTRY_SERVICE}" \
    "${REGISTRY_FORWARD_PORT}:80" >"$WORK_DIR/registry-port-forward.log" 2>&1 &
  REGISTRY_FORWARD_PID=$!

  local deadline=$((SECONDS + 30))
  while (( SECONDS < deadline )); do
    if ! kill -0 "$REGISTRY_FORWARD_PID" >/dev/null 2>&1; then
      fail "registry port-forward exited unexpectedly"
    fi
    if grep -q 'Forwarding from 127.0.0.1' "$WORK_DIR/registry-port-forward.log"; then
      return
    fi
    sleep 1
  done
  fail "timed out waiting for registry port-forward"
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

main() {
  need_cmd kubectl
  need_cmd go
  need_cmd curl
  require_allowed_kubeconfig

  WORK_DIR="$(mktemp -d)"
  local node_ip output_image policy_file build_job built_image applied_image deployment_image
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
  (
    cd "$ROOT_DIR"
    KUBECONFIG="$KUBECONFIG_PATH" go run ./cmd/main.go \
      --metrics-bind-address=0 \
      --health-probe-bind-address=:0
  ) >"$WORK_DIR/manager.log" 2>&1 &
  MANAGER_PID=$!
  wait_for_manager

  log "applying demo Deployment and ConfigMap"
  kubectl_cmd -n "$NAMESPACE" apply -f "$ROOT_DIR/test/e2e/manifests/context-configmap.yaml"
  kubectl_cmd -n "$NAMESPACE" apply -f "$ROOT_DIR/test/e2e/manifests/deployment.yaml"
  kubectl_cmd -n "$NAMESPACE" rollout status "deployment/${DEPLOYMENT_NAME}" --timeout="$ROLLOUT_TIMEOUT"

  policy_file="$(render_policy "$output_image")"
  log "applying Always policy ${POLICY_NAME}"
  kubectl_cmd -n "$NAMESPACE" apply -f "$policy_file"

  build_job="$(wait_for_build_job_name)"
  log "waiting for real Kaniko Build Job ${build_job}"
  kubectl_cmd -n "$NAMESPACE" wait --for=condition=complete "job/${build_job}" --timeout="$BUILD_TIMEOUT" || fail "Build Job ${build_job} did not complete"

  wait_for_jsonpath_value "policy phase" "Succeeded" \
    -n "$NAMESPACE" get altimageupdatepolicy "$POLICY_NAME" -o jsonpath='{.status.phase}'

  built_image="$(kubectl_cmd -n "$NAMESPACE" get altimageupdatepolicy "$POLICY_NAME" -o jsonpath='{.status.lastBuiltImage}')"
  applied_image="$(kubectl_cmd -n "$NAMESPACE" get altimageupdatepolicy "$POLICY_NAME" -o jsonpath='{.status.lastAppliedImage}')"
  [[ -n "$built_image" ]] || fail "status.lastBuiltImage is empty"
  [[ -n "$applied_image" ]] || fail "status.lastAppliedImage is empty"
  [[ "$built_image" == "$applied_image" ]] || fail "lastBuiltImage and lastAppliedImage differ: ${built_image} != ${applied_image}"

  verify_registry_tag "$built_image"

  deployment_image="$(kubectl_cmd -n "$NAMESPACE" get deployment "$DEPLOYMENT_NAME" \
    -o 'go-template={{range .spec.template.spec.containers}}{{if eq .name "'"$CONTAINER_NAME"'"}}{{.image}}{{end}}{{end}}')"
  [[ "$deployment_image" == "$applied_image" ]] || fail "Deployment image ${deployment_image} does not equal status.lastAppliedImage ${applied_image}"

  kubectl_cmd -n "$NAMESPACE" rollout status "deployment/${DEPLOYMENT_NAME}" --timeout="$ROLLOUT_TIMEOUT"
  wait_for_jsonpath_value "rollout condition" "True" \
    -n "$NAMESPACE" get altimageupdatepolicy "$POLICY_NAME" -o jsonpath='{.status.conditions[?(@.type=="RolloutCompleted")].status}'

  log "DONE: Build Job ${build_job} pushed ${built_image}, Deployment uses ${deployment_image}, policy phase is Succeeded"
}

main "$@"
