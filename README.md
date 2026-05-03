# alt-image-update-operator

`alt-image-update-operator` is a Kubernetes operator for security-oriented rebuilds of ALT Linux based container images. A user describes an `AltImageUpdatePolicy`, and the controller checks the policy, runs real Kubernetes Jobs for ALT package checking and Kaniko image building, pushes the rebuilt image to a registry, patches one container in a target Deployment, observes rollout, and records the result in policy status.

The project is built with Go, Kubebuilder, and controller-runtime. The CRD API group is `security.altlinux.org`, API version is `v1alpha1`, and the main namespaced kind is `AltImageUpdatePolicy`.

Runtime and e2e paths are intentionally real: build success is not faked by the controller or by `hack/e2e.sh`. The default builder is Kaniko, and the demo/e2e workflow pushes an actual image to the configured registry.

## Prerequisites

- Go `1.25.7` or newer, matching `go.mod`.
- `make`.
- `kubectl` for cluster installation and inspection.
- Access to a Kubernetes cluster where it is safe to install CRDs, Jobs, RBAC, and a controller.
- A container registry reachable by both Kaniko build Pods and the target Deployment Pods.
- Optional: Docker or another container tool if you build and push the manager image with `make docker-build docker-push`.

For this repository's live e2e validation, do not use the laptop default kubeconfig. The supported live target is the explicitly allowed remote minikube kubeconfig:

```sh
export KUBECONFIG="$HOME/.kube-remote/minikube-config"
kubectl --kubeconfig "$KUBECONFIG" get nodes
```

`hack/e2e.sh` refuses other kubeconfig paths and verifies the remote minikube context before it starts.

## Install Basics

Generate manifests and run tests locally:

```sh
make manifests
go test ./...
```

Install only the CRD into the selected cluster:

```sh
make install
```

Build and push the controller image, then deploy the manager:

```sh
make docker-build docker-push IMG=<registry>/alt-image-update-operator:<tag>
make deploy IMG=<registry>/alt-image-update-operator:<tag>
```

For development against an allowed cluster, you can also install the CRD and run the controller locally:

```sh
make install
go run ./cmd/main.go
```

Apply the demo namespace, Deployment, ConfigMap build context, and policy examples:

```sh
kubectl apply -f config/demo/namespace.yaml
kubectl apply -f config/demo/context-configmap.yaml
kubectl apply -f config/demo/deployment.yaml
kubectl apply -f config/demo/policy-always.yaml
```

## Minimal Policy

This example always rebuilds an ALT Linux image from a Dockerfile stored in a ConfigMap and updates container `app` in Deployment `demo-app`.

```yaml
apiVersion: security.altlinux.org/v1alpha1
kind: AltImageUpdatePolicy
metadata:
  name: demo-app-always
  namespace: alt-image-update-demo
spec:
  alt:
    branch: p10
    baseImage: registry.altlinux.org/alt/alt:p10
  check:
    mode: Always
  build:
    builder: Kaniko
    outputImage: localhost:5001/alt/demo-app
    context:
      type: ConfigMap
      configMapRef:
        name: demo-app-context
        dockerfileKey: Dockerfile
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: demo-app
  containerName: app
```

Useful optional fields:

- `spec.trigger.manualToken`: change this value to force a new idempotent run.
- `spec.check.mode`: `Always` or `AltAptSimulation`.
- `spec.build.builderImage`: override the Kaniko executor image.
- `spec.build.registrySecretRef.name`: mount Docker registry credentials from a Secret containing `.dockerconfigjson`.
- `spec.rollout.timeoutSeconds`: bound Deployment rollout observation.
- `spec.jobTemplate.hostNetwork`: useful for node-local demo registries such as `localhost:5000`.
- `spec.jobTemplate.ttlSecondsAfterFinished`, `backoffLimit`, `activeDeadlineSeconds`, and `serviceAccountName`: common settings for Check and Build Jobs.

## Status

The controller writes Kubernetes-style status, including:

- `status.phase`: `Pending`, `Checking`, `UpToDate`, `Building`, `Publishing`, `Applying`, `RollingOut`, `Succeeded`, or `Failed`.
- `status.conditions`: `Ready`, `CheckCompleted`, `UpdatesAvailable`, `BuildCompleted`, `ImagePublished`, `DeploymentUpdated`, `RolloutCompleted`, `UpToDate`, and `Failed` conditions.
- `status.lastCheckJobName`, `status.lastBuildJobName`, `status.lastBuiltImage`, `status.lastAppliedImage`, and stage timestamps.
- `status.currentRunKey` and `status.buildID`, which make repeated reconciles idempotent for the same policy generation and trigger token.

Inspect a policy with:

```sh
kubectl -n alt-image-update-demo get altimageupdatepolicy demo-app-always -o yaml
```

## E2E

Run the full remote-minikube demo pipeline with:

```sh
export KUBECONFIG="$HOME/.kube-remote/minikube-config"
hack/e2e.sh
```

The script installs CRDs, starts the controller manager locally against the allowed remote minikube, applies the demo manifests, verifies a real Kaniko build and registry push, checks the Deployment image and rollout, and then runs the `AltAptSimulation` path.

## Architecture

See [docs/architecture.md](docs/architecture.md) for the implemented reconcile state machine, Job model, Deployment patch behavior, status fields, and MVP limitations.

## MVP Limitations

- No security-only CVE filtering. `AltAptSimulation` classifies apt simulation output as package changes or no changes.
- No Git build context. The MVP build context source is a Kubernetes ConfigMap.
- No rollback automation after a bad rollout.
- No non-Deployment workload support. The target must be an `apps/v1` Deployment in the same namespace as the policy.
- Only Kaniko is implemented as a builder.

## Uninstall

Delete demo resources and uninstall the CRD/controller from the selected cluster:

```sh
kubectl delete -f config/demo/policy-always.yaml --ignore-not-found
kubectl delete -f config/demo/policy-alt-apt-simulation.yaml --ignore-not-found
kubectl delete -f config/demo/deployment.yaml --ignore-not-found
kubectl delete -f config/demo/context-configmap.yaml --ignore-not-found
make undeploy ignore-not-found=true
make uninstall ignore-not-found=true
```

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0.
