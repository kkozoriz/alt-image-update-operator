# Architecture

`alt-image-update-operator` reconciles a namespaced `AltImageUpdatePolicy` into a real image rebuild and a declarative Deployment image update. The MVP path is:

```text
AltImageUpdatePolicy
  -> preflight validation
  -> optional AltAptSimulation Check Job
  -> Kaniko Build Job
  -> registry push
  -> target Deployment patch
  -> rollout observation
  -> status and Events
```

The controller does not manufacture successful runtime outcomes. Check and Build stages are Kubernetes Jobs, Kaniko performs the image build and push, and rollout success comes from the target Deployment status.

## CRD Model

The CRD is `security.altlinux.org/v1alpha1`, kind `AltImageUpdatePolicy`, with namespaced scope.

Important `spec` fields:

- `spec.alt.branch`: ALT branch, one of `p10`, `p11`, or `sisyphus`.
- `spec.alt.baseImage`: ALT Linux image used by Check Jobs and passed to builds as `ALT_BASE_IMAGE`.
- `spec.trigger.manualToken`: optional user-controlled token. Changing it starts a new run.
- `spec.check.mode`: `Always` or `AltAptSimulation`.
- `spec.build.builder`: `Kaniko`.
- `spec.build.builderImage`: optional Kaniko executor image override.
- `spec.build.outputImage`: output repository. The controller replaces any tag with a deterministic build tag.
- `spec.build.context.type`: `ConfigMap`.
- `spec.build.context.configMapRef.name`: ConfigMap containing build context files.
- `spec.build.context.configMapRef.dockerfileKey`: key in the ConfigMap used as the Dockerfile.
- `spec.build.registrySecretRef.name`: optional Docker config Secret mounted at `/kaniko/.docker/config.json`.
- `spec.targetRef`: target workload reference. MVP supports only same-namespace `apps/v1`, `Deployment`.
- `spec.containerName`: regular Deployment container to update.
- `spec.rollout.timeoutSeconds`: optional rollout timeout.
- `spec.jobTemplate`: common Check and Build Job settings such as `serviceAccountName`, `hostNetwork`, `ttlSecondsAfterFinished`, `backoffLimit`, and `activeDeadlineSeconds`.

The controller validates runtime prerequisites before starting Jobs:

- target Deployment exists;
- target Deployment contains `spec.containerName`;
- build context ConfigMap exists;
- ConfigMap contains the configured Dockerfile key;
- output image can be converted into a build-specific image reference.

## Run Identity and Idempotency

Each reconcile computes:

- `status.currentRunKey`: a SHA-256 key from policy generation, manual trigger token, target identity, build context identity, output image, and tag template.
- `status.buildID`: a compact tag-safe identifier derived from the run key and generation.

Job names and image tags are based on `buildID`. Repeated reconciles for the same current run find existing Jobs instead of creating duplicates. A terminal current run in `Succeeded`, `Failed`, or `UpToDate` is not reprocessed. Changing `spec.trigger.manualToken` or another run identity input starts a new run and clears run-scoped status fields.

## Check Stage

`spec.check.mode: Always` skips the check gate and goes directly to the build pipeline.

`spec.check.mode: AltAptSimulation` creates a Check Job with:

- container name `alt-apt-simulation`;
- image `spec.alt.baseImage`;
- command that prints and executes `apt-get update`, then prints and executes `apt-get -s dist-upgrade`;
- labels and annotations tying the Job to the policy, run key, build ID, and job type.

When the Check Job completes, the controller finds a pod owned by the Job and reads the check container logs via the Kubernetes pods/log API. The parser conservatively classifies the log:

- `UpdatesAvailable`: package-change evidence was found, so the build pipeline continues;
- `NoUpdates`: apt simulation completed without package-change evidence, so the policy becomes `UpToDate`;
- `CheckFailed`: the Job failed or apt error evidence was found, so the policy becomes `Failed`.

Raw apt logs are not copied into status.

## Build Stage

The Build Job is a real Kaniko Job. The controller creates it from `spec.build` and the computed built image reference.

The Kaniko container:

- mounts the ConfigMap build context at `/workspace`;
- uses `/workspace/<dockerfileKey>` as the Dockerfile;
- pushes `spec.build.outputImage` with the deterministic `buildID` tag;
- writes an image digest file under `/results`;
- receives `ALT_BASE_IMAGE` and `ALT_BRANCH` as build arguments.

If `spec.build.registrySecretRef` is set, the Secret is mounted as Kaniko Docker config. The controller must not log registry credential or Secret data.

The controller observes Job conditions:

- running or present Job -> `Building` and requeue;
- `Complete=True` -> records `status.lastBuiltImage` and continues to Deployment patching;
- `Failed=True` -> `Failed` with build failure conditions.

## Deployment Patch

After a completed Build Job, the controller patches only the named container in the target Deployment:

```text
spec.template.spec.containers[?name == spec.containerName].image
```

Other containers are preserved. The pod template also receives annotations:

- `security.altlinux.org/last-build-id`;
- `security.altlinux.org/last-built-image`;
- `security.altlinux.org/last-applied-at`;
- `security.altlinux.org/policy-name`;
- `security.altlinux.org/policy-namespace`.

If the Deployment already has the desired image and annotations, no patch is sent. This keeps the apply stage idempotent and avoids repeated rollouts.

## Rollout Observation

The controller observes the target Deployment after patching. It waits for the Deployment controller to observe the target generation, then checks updated, available, total, and unavailable replica counts.

Rollout succeeds when desired replicas are updated, present, available, and there are no unavailable replicas. Rollout fails on progress deadline exceeded, replica failure, or the configured timeout. Progressing rollouts requeue without changing the desired image again.

## Status and Events

The policy status records the active phase, reason, human-readable message, timestamps, Job names, image references, and Kubernetes-style conditions.

Main phases:

- `Pending`: preflight/run setup state.
- `Checking`: Check Job exists and is not terminal.
- `UpToDate`: `AltAptSimulation` found no package updates for the current run.
- `Building`: Build Job exists and is not terminal.
- `Publishing`: CRD phase value for image publishing; the current Kaniko pipeline records push completion with `ImagePublished=True` and proceeds to `Applying`.
- `Applying`: build completed and the controller is applying the image.
- `RollingOut`: target Deployment image was applied and rollout is being observed.
- `Succeeded`: rollout completed.
- `Failed`: preflight, check, build, apply, or rollout failed.

Important status fields:

- `observedGeneration`;
- `phase`;
- `buildID`;
- `currentRunKey`;
- `lastCheckTime`;
- `lastBuildStartTime`;
- `lastBuildCompletionTime`;
- `lastApplyTime`;
- `lastRolloutTime`;
- `lastCheckJobName`;
- `lastBuildJobName`;
- `lastBuiltImage`;
- `lastAppliedImage`;
- `targetDeploymentGeneration`;
- `reason`;
- `message`;
- `conditions`.

The controller also emits Kubernetes Events for check, build, Deployment update, rollout, success, and failure milestones. Metrics count build and rollout outcomes with low-cardinality labels.

## RBAC and Ownership

The manager RBAC includes access to:

- `AltImageUpdatePolicy` resources and status;
- Jobs;
- Pods and pods/log;
- Deployments;
- Events;
- ConfigMaps;
- Secrets.

Jobs created by the controller have owner references to the policy where Kubernetes permits them, plus stable labels and annotations for discovery.

## Demo and E2E

`hack/e2e.sh` is the supported full workflow for the current project environment. It verifies the allowed remote minikube kubeconfig, installs CRDs, runs the local controller manager, applies demo resources, waits for real Check and Build Jobs, verifies registry tags, verifies Deployment image updates and rollout, and checks final policy status.

The demo manifests live under `config/demo/`; e2e-specific manifests live under `test/e2e/manifests/`.

## MVP Limitations

- No security-only CVE filtering. `AltAptSimulation` detects package-change evidence from apt simulation output, not CVE-specific update intent.
- No Git build context. ConfigMap build context is the only implemented source.
- No rollback automation. Rollout failures are reported in status, but the controller does not restore the previous image.
- No non-Deployment workloads. The target must be an `apps/v1` Deployment.
- No cross-namespace target updates. The policy and target Deployment are reconciled in the same namespace.
- No builder other than Kaniko.
- No webhook-based validation. Validation is provided by CRD OpenAPI markers and controller preflight checks.
