# Demo Workflow

This document is the thesis-demo runbook for `alt-image-update-operator`. It uses the real controller, real Kubernetes Jobs, real Kaniko image build and push, and real Deployment rollout checks.

Do not use the laptop default kubeconfig for live demo work. The supported live target for this repository is the remote minikube kubeconfig:

```sh
export KUBECONFIG="$HOME/.kube-remote/minikube-config"
kubectl --kubeconfig "$KUBECONFIG" get nodes -o wide
```

Expected remote-minikube shape:

```text
NAME       STATUS   ROLES           AGE   VERSION   INTERNAL-IP
minikube   Ready    control-plane   ...   ...       192.168.49.2
```

If the command cannot connect to `https://127.0.0.1:16443`, start the SSH tunnel to the remote host before running live checks:

```sh
ssh -N -L 127.0.0.1:16443:192.168.49.2:8443 clouduser@185.120.186.138
```

## Full E2E Demo

The most reliable clean-cluster demonstration is the scripted remote-minikube path:

```sh
export KUBECONFIG="$HOME/.kube-remote/minikube-config"
E2E_REGISTRY_FORWARD_PORT=5021 hack/e2e.sh
```

`hack/e2e.sh` verifies the allowed kubeconfig, applies `config/crd`, recreates namespace `alt-image-update-demo`, starts a freshly built controller manager binary against the remote minikube, applies `test/e2e/manifests/context-configmap.yaml` and `test/e2e/manifests/deployment.yaml`, runs the `Always` policy, resets registry storage, then runs the `AltAptSimulation` policy.

Successful end of the script:

```text
[e2e] policy demo-app-always completed: Build Job ... pushed localhost:5000/alt/demo-app:...
[e2e] Check Job ... logs show apt-get update and apt-get -s dist-upgrade
[e2e] policy demo-app-alt-apt-simulation completed: Build Job ... pushed localhost:5000/alt/demo-app:...
[e2e] DONE: Always and AltAptSimulation e2e scenarios passed in namespace alt-image-update-demo
```

## Manual Clean-Cluster Demo

Use this path when you want to show each Kubernetes object and status field manually. Keep all commands in a shell where `KUBECONFIG` points at the allowed remote minikube.

Install the CRD and create a clean demo namespace:

```sh
kubectl --kubeconfig "$KUBECONFIG" apply -k config/crd
kubectl --kubeconfig "$KUBECONFIG" delete namespace alt-image-update-demo --ignore-not-found=true --wait=true
kubectl --kubeconfig "$KUBECONFIG" apply -f config/demo/namespace.yaml
```

Expected CRD check:

```sh
kubectl --kubeconfig "$KUBECONFIG" get crd altimageupdatepolicies.security.altlinux.org \
  -o jsonpath='{.spec.group}{" "}{.spec.names.kind}{" "}{.spec.versions[?(@.name=="v1alpha1")].served}{"\n"}'
```

```text
security.altlinux.org AltImageUpdatePolicy true
```

Start the controller. For a thesis demo on the current remote minikube, the local-manager mode matches `hack/e2e.sh` and avoids needing a pushed manager image:

```sh
KUBECONFIG="$KUBECONFIG" go run ./cmd/main.go \
  --metrics-bind-address=0 \
  --health-probe-bind-address=:0
```

If you have already built and pushed a manager image reachable by the remote minikube, you can show a controller Pod instead:

```sh
make deploy IMG=<registry>/alt-image-update-operator:<tag>
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-operator-system rollout status \
  deployment/alt-image-update-operator-controller-manager --timeout=120s
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-operator-system get pods \
  -l control-plane=controller-manager
```

Expected controller Pod shape:

```text
NAME                                                        READY   STATUS    RESTARTS   AGE
alt-image-update-operator-controller-manager-...            1/1     Running   0          ...
```

Apply the demo Deployment and ConfigMap build context from `config/demo`:

```sh
kubectl --kubeconfig "$KUBECONFIG" apply -f config/demo/context-configmap.yaml
kubectl --kubeconfig "$KUBECONFIG" apply -f config/demo/deployment.yaml
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo rollout status deployment/demo-app --timeout=180s
```

Check the initial target image:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get deployment demo-app \
  -o 'go-template={{range .spec.template.spec.containers}}{{if eq .name "app"}}{{.image}}{{"\n"}}{{end}}{{end}}'
```

```text
registry.altlinux.org/alt/alt:p10
```

For the remote-minikube registry, render the `Always` policy with `localhost:5000`, which is the image repository used by `hack/e2e.sh`. The checked-in demo policy uses `localhost:5001` for generic local-registry demos.

```sh
tmp_policy="$(mktemp)"
sed 's#outputImage: .*#outputImage: localhost:5000/alt/demo-app#' \
  config/demo/policy-always.yaml >"$tmp_policy"
kubectl --kubeconfig "$KUBECONFIG" apply -f "$tmp_policy"
rm -f "$tmp_policy"
```

Verify the policy, Build Job, Kaniko logs, registry tag, Deployment image, rollout, and CR status:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{.status.phase}{" "}{.status.lastBuildJobName}{"\n"}'
```

Expected while running:

```text
Building aiuo-build-demo-app-always-...
```

Get the Build Job name by label:

```sh
build_job="$(
  kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get jobs \
    -l security.altlinux.org/job-type=build,security.altlinux.org/policy-name=demo-app-always \
    -o jsonpath='{.items[0].metadata.name}'
)"
printf '%s\n' "$build_job"
```

Expected:

```text
aiuo-build-demo-app-always-...
```

Wait for the real Kaniko Job:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo wait --for=condition=Complete \
  "job/${build_job}" --timeout=900s
```

Expected:

```text
job.batch/aiuo-build-demo-app-always-... condition met
```

Show Kaniko evidence:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo logs "job/${build_job}" -c kaniko --tail=80
```

Expected log evidence includes lines such as:

```text
INFO[... ] Retrieving image manifest registry.altlinux.org/alt/alt:p10
INFO[... ] Taking snapshot of full filesystem...
INFO[... ] Pushing image to localhost:5000/alt/demo-app:...
INFO[... ] Pushed localhost:5000/alt/demo-app@sha256:...
```

Port-forward the remote minikube registry service and verify the pushed tag:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n kube-system port-forward --address 127.0.0.1 svc/registry 5021:80 >/tmp/alt-registry-pf.log 2>&1 &
registry_pf=$!
sleep 3
built_image="$(
  kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
    -o jsonpath='{.status.lastBuiltImage}'
)"
repo="${built_image#*/}"
tag="${repo##*:}"
repo="${repo%:*}"
curl -fsS "http://127.0.0.1:5021/v2/${repo}/tags/list"
kill "$registry_pf"
```

Expected registry response:

```json
{"name":"alt/demo-app","tags":["<build-id-tag>"]}
```

Wait for rollout and compare Deployment image to policy status:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo rollout status deployment/demo-app --timeout=300s
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get deployment demo-app \
  -o 'go-template={{range .spec.template.spec.containers}}{{if eq .name "app"}}{{.image}}{{"\n"}}{{end}}{{end}}'
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{.status.lastAppliedImage}{"\n"}'
```

Expected:

```text
deployment "demo-app" successfully rolled out
localhost:5000/alt/demo-app:<build-id-tag>
localhost:5000/alt/demo-app:<build-id-tag>
```

Final policy status check:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{.status.phase}{" "}{.status.conditions[?(@.type=="Ready")].status}{" "}{.status.conditions[?(@.type=="RolloutCompleted")].status}{"\n"}'
```

```text
Succeeded True True
```

## AltAptSimulation Demo

Apply the `AltAptSimulation` policy from `config/demo`, again rendering `localhost:5000` for remote minikube:

```sh
tmp_policy="$(mktemp)"
sed 's#outputImage: .*#outputImage: localhost:5000/alt/demo-app#' \
  config/demo/policy-alt-apt-simulation.yaml >"$tmp_policy"
kubectl --kubeconfig "$KUBECONFIG" apply -f "$tmp_policy"
rm -f "$tmp_policy"
```

Get and wait for the Check Job:

```sh
check_job="$(
  kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get jobs \
    -l security.altlinux.org/job-type=check,security.altlinux.org/policy-name=demo-app-alt-apt-simulation \
    -o jsonpath='{.items[0].metadata.name}'
)"
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo wait --for=condition=Complete \
  "job/${check_job}" --timeout=900s
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo logs "job/${check_job}" \
  -c alt-apt-simulation | grep -E 'RUN apt-get update|RUN apt-get -s dist-upgrade'
```

Expected Check Job evidence:

```text
RUN apt-get update
RUN apt-get -s dist-upgrade
```

Check the controller's classification:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-alt-apt-simulation \
  -o jsonpath='{.status.phase}{" "}{.status.conditions[?(@.type=="CheckCompleted")].reason}{"\n"}'
```

Expected when updates are detected:

```text
Building UpdatesAvailable
```

Expected when no updates are detected:

```text
UpToDate NoUpdates
```

If the result is `UpdatesAvailable`, repeat the Build Job, registry, Deployment image, rollout, and final status checks from the `Always` section with policy name `demo-app-alt-apt-simulation`.

## Cleanup

```sh
kubectl --kubeconfig "$KUBECONFIG" delete -f config/demo/policy-alt-apt-simulation.yaml --ignore-not-found=true
kubectl --kubeconfig "$KUBECONFIG" delete -f config/demo/policy-always.yaml --ignore-not-found=true
kubectl --kubeconfig "$KUBECONFIG" delete namespace alt-image-update-demo --ignore-not-found=true --wait=true
make undeploy ignore-not-found=true
make uninstall ignore-not-found=true
```
