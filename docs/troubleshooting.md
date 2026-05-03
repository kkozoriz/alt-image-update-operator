# Troubleshooting

Use these checks against the explicitly allowed remote minikube only:

```sh
export KUBECONFIG="$HOME/.kube-remote/minikube-config"
kubectl --kubeconfig "$KUBECONFIG" get nodes -o wide
```

All examples use namespace `alt-image-update-demo`, Deployment `demo-app`, container `app`, and policies `demo-app-always` or `demo-app-alt-apt-simulation`.

## First Commands

Collect the high-signal state:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicies -o wide
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get jobs
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get pods
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get events --sort-by=.metadata.creationTimestamp
```

Policy phase, reason, and key outputs:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{.status.phase}{" "}{.status.reason}{" "}{.status.lastCheckJobName}{" "}{.status.lastBuildJobName}{" "}{.status.lastBuiltImage}{" "}{.status.lastAppliedImage}{"\n"}'
```

Expected healthy terminal shape:

```text
Succeeded RolloutCompleted <empty-check-job> aiuo-build-demo-app-always-... localhost:5000/alt/demo-app:... localhost:5000/alt/demo-app:...
```

Failed condition details:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{range .status.conditions[?(@.type=="Failed")]}{.status}{" "}{.reason}{" "}{.message}{"\n"}{end}'
```

Expected failed shape:

```text
True BuildJobFailed Build Job ... failed
```

## Registry Unavailable

Symptoms:

- `hack/e2e.sh` fails around `registry port-forward`, `registry endpoint`, or `registry tags endpoint`.
- `curl http://127.0.0.1:<port>/v2/.../tags/list` fails.
- Build may have succeeded, but tag verification cannot reach the registry service.

Check the remote minikube registry service:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n kube-system get svc registry
kubectl --kubeconfig "$KUBECONFIG" -n kube-system get endpoints registry \
  -o jsonpath='{.subsets[0].addresses[0].ip}{"\n"}'
kubectl --kubeconfig "$KUBECONFIG" -n kube-system get pods -l actual-registry=true
kubectl --kubeconfig "$KUBECONFIG" -n kube-system rollout status deployment/registry --timeout=120s
```

Expected:

```text
<non-empty endpoint IP>
deployment "registry" successfully rolled out
```

If the endpoint is empty, restart or enable the minikube registry addon on the remote host, not on the laptop default cluster. If disk pressure is suspected, clear old registry data on the remote minikube before rerunning `hack/e2e.sh`.

## Kaniko Push Failed

Symptoms:

- Policy phase is `Failed`.
- `status.reason` is `BuildJobFailed`.
- Build Job condition is `Failed=True`.
- Kaniko logs contain push, auth, DNS, TLS, or registry connection errors.

Find the Build Job and logs:

```sh
build_job="$(
  kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get jobs \
    -l security.altlinux.org/job-type=build,security.altlinux.org/policy-name=demo-app-always \
    -o jsonpath='{.items[0].metadata.name}'
)"
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo describe job "$build_job"
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo logs "job/${build_job}" -c kaniko --tail=160
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{.status.phase}{" "}{.status.reason}{" "}{.status.conditions[?(@.type=="BuildCompleted")].status}{"\n"}'
```

Expected failed shape:

```text
Failed BuildJobFailed False
```

Common fixes:

- For remote minikube, use `spec.build.outputImage: localhost:5000/alt/demo-app` and `spec.jobTemplate.hostNetwork: true`.
- For a private registry, set `spec.build.registrySecretRef.name` to a valid Docker config Secret in `alt-image-update-demo`.
- Confirm the Kaniko image in `spec.build.builderImage` can be pulled by the cluster.

## Deployment Pull Failed

Symptoms:

- Build Job is complete and policy has `lastBuiltImage`.
- Deployment rollout does not complete.
- New ReplicaSet Pods show `ImagePullBackOff`, `ErrImagePull`, or HTTP/HTTPS registry mismatch.

Inspect Deployment and Pods:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get deployment demo-app \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="app")].image}{"\n"}'
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get pods -l app.kubernetes.io/name=demo-app
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo describe deployment demo-app
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo describe pods -l app.kubernetes.io/name=demo-app
```

Expected healthy image match:

```text
localhost:5000/alt/demo-app:<build-id-tag>
```

Common fixes:

- For remote minikube, do not use `192.168.49.2:5000` in the policy. Kubelet may try HTTPS against the HTTP registry. Use `localhost:5000` with `jobTemplate.hostNetwork: true`.
- If using an external private registry, add the required image pull secret to the target Deployment service account or pod template.
- Check that `imagePullPolicy` does not force a tag that is absent from the registry.

## Check Job Failed

Symptoms:

- `AltAptSimulation` policy phase is `Failed`.
- `status.reason` is `CheckJobFailed`, `CheckLogsUnavailable`, `AptCommandFailed`, or `CheckFailed`.
- Check Job logs show apt mirror or repository errors.

Inspect the Check Job:

```sh
check_job="$(
  kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get jobs \
    -l security.altlinux.org/job-type=check,security.altlinux.org/policy-name=demo-app-alt-apt-simulation \
    -o jsonpath='{.items[0].metadata.name}'
)"
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo describe job "$check_job"
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo logs "job/${check_job}" -c alt-apt-simulation --tail=200
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-alt-apt-simulation \
  -o jsonpath='{.status.phase}{" "}{.status.reason}{" "}{.status.conditions[?(@.type=="CheckCompleted")].status}{"\n"}'
```

Expected failed shape:

```text
Failed CheckJobFailed False
```

Common fixes:

- Confirm `spec.alt.baseImage` is a real ALT image, for example `registry.altlinux.org/alt/alt:p10`.
- Confirm the cluster can reach ALT package mirrors from Pods.
- If logs are unavailable, inspect Check Job Pods and RBAC for `pods/log` access by the controller.

## ConfigMap Missing

Symptoms:

- No Check or Build Job is created.
- Policy phase is `Failed`.
- `status.reason` is `BuildContextNotFound`.

Check the configured ConfigMap:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{.spec.build.context.configMapRef.name}{"\n"}'
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get configmap demo-app-context
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{.status.phase}{" "}{.status.reason}{"\n"}'
```

Expected failed shape:

```text
Failed BuildContextNotFound
```

Fix:

```sh
kubectl --kubeconfig "$KUBECONFIG" apply -f config/demo/context-configmap.yaml
```

Then change `spec.trigger.manualToken` on the policy to start a new run.

## Dockerfile Key Missing

Symptoms:

- No Check or Build Job is created.
- Policy phase is `Failed`.
- `status.reason` is `DockerfileKeyNotFound`.

Check the key name and available ConfigMap data keys:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{.spec.build.context.configMapRef.dockerfileKey}{"\n"}'
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get configmap demo-app-context \
  -o jsonpath='{range $k,$v := .data}{$k}{"\n"}{end}'
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{.status.phase}{" "}{.status.reason}{"\n"}'
```

Expected failed shape:

```text
Dockerfile
Containerfile
Failed DockerfileKeyNotFound
```

The first line is the configured Dockerfile key; the next line or lines are the actual ConfigMap data keys. In the healthy case the actual key list includes `Dockerfile`; in the failed case it does not. Fix the ConfigMap or update `spec.build.context.configMapRef.dockerfileKey`, then change `spec.trigger.manualToken`.

## Rollout Timeout

Symptoms:

- Build Job completed.
- Deployment image was patched.
- Policy phase becomes `Failed` with `status.reason=RolloutFailed`, or remains `RollingOut` until `spec.rollout.timeoutSeconds` is exceeded.

Inspect rollout state:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo rollout status deployment/demo-app --timeout=30s
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get deployment demo-app \
  -o jsonpath='{.metadata.generation}{" "}{.status.observedGeneration}{" "}{.status.updatedReplicas}{" "}{.status.availableReplicas}{" "}{.status.unavailableReplicas}{"\n"}'
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{.status.phase}{" "}{.status.reason}{" "}{.status.conditions[?(@.type=="RolloutCompleted")].status}{"\n"}'
```

Expected healthy shape:

```text
deployment "demo-app" successfully rolled out
<generation> <same-generation> 1 1 <empty-unavailable-replicas>
Succeeded RolloutCompleted True
```

Common fixes:

- Read Pod events for image pull failures, crash loops, or readiness probe failures.
- Confirm the built image command can run as the configured non-root user.
- Increase `spec.rollout.timeoutSeconds` only after confirming the Deployment is making progress.
- Change `spec.trigger.manualToken` after fixing the image or Deployment to start a new run.

## Re-Run a Policy

Terminal current runs are idempotent. To retry after fixing an input, change the manual token:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo patch altimageupdatepolicy demo-app-always \
  --type=merge \
  -p "{\"spec\":{\"trigger\":{\"manualToken\":\"retry-$(date -u +%Y%m%dT%H%M%SZ)\"}}}"
```

Expected new-run signal:

```sh
kubectl --kubeconfig "$KUBECONFIG" -n alt-image-update-demo get altimageupdatepolicy demo-app-always \
  -o jsonpath='{.status.phase}{" "}{.status.buildID}{" "}{.status.lastBuildJobName}{"\n"}'
```

```text
Building <new-build-id> aiuo-build-demo-app-always-...
```
