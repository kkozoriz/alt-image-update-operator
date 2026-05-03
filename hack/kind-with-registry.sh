#!/usr/bin/env bash
set -euo pipefail

cat >&2 <<'EOF'
Local kind registry setup is intentionally disabled for this project.
Use the explicitly allowed remote minikube workflow instead:

  KUBECONFIG=$HOME/.kube-remote/minikube-config hack/e2e.sh
EOF
exit 2
