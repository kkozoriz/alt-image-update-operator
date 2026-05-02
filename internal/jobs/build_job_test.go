package jobs

import (
	"strings"
	"testing"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	buildJobRunKey     = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	buildJobBuildID    = "g7-aaaaaaaaaaaaaaaa"
	buildJobBuiltImage = "localhost:5001/alt/demo-app:g7-aaaaaaaaaaaaaaaa"
)

func TestNewBuildJobCreatesRealKanikoJob(t *testing.T) {
	policy := testBuildPolicy()

	job, err := NewBuildJob(policy, testBuildJobOptions())
	if err != nil {
		t.Fatalf("NewBuildJob() error = %v", err)
	}

	if job.Name != BuildJobName(policy.Name, buildJobBuildID) {
		t.Fatalf("job name = %q, want %q", job.Name, BuildJobName(policy.Name, buildJobBuildID))
	}
	if job.Namespace != policy.Namespace {
		t.Fatalf("job namespace = %q, want %q", job.Namespace, policy.Namespace)
	}
	if job.Labels[LabelJobType] != string(JobTypeBuild) {
		t.Fatalf("job type label = %q, want build", job.Labels[LabelJobType])
	}
	if job.Annotations[AnnotationRunKey] != buildJobRunKey {
		t.Fatalf("run key annotation = %q, want %q", job.Annotations[AnnotationRunKey], buildJobRunKey)
	}

	if len(job.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences len = %d, want 1", len(job.OwnerReferences))
	}
	owner := job.OwnerReferences[0]
	if owner.APIVersion != securityv1alpha1.GroupVersion.String() || owner.Kind != "AltImageUpdatePolicy" || owner.Name != policy.Name || owner.UID != policy.UID {
		t.Fatalf("ownerReference = %#v, want policy owner", owner)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Fatalf("ownerReference controller = %v, want true", owner.Controller)
	}
	if owner.BlockOwnerDeletion == nil || !*owner.BlockOwnerDeletion {
		t.Fatalf("ownerReference blockOwnerDeletion = %v, want true", owner.BlockOwnerDeletion)
	}

	podSpec := job.Spec.Template.Spec
	if podSpec.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("restartPolicy = %q, want Never", podSpec.RestartPolicy)
	}
	container := onlyBuildContainer(t, job)
	if container.Image != policy.Spec.Build.BuilderImage {
		t.Fatalf("kaniko image = %q, want %q", container.Image, policy.Spec.Build.BuilderImage)
	}
	assertContains(t, container.Args, "--context=dir://"+WorkspaceMountPath)
	assertContains(t, container.Args, "--dockerfile="+WorkspaceMountPath+"/Dockerfile")
	assertContains(t, container.Args, "--destination="+buildJobBuiltImage)
	assertContains(t, container.Args, "--digest-file="+ResultsMountPath+"/image-digest")
	assertContains(t, container.Args, "--build-arg=ALT_BASE_IMAGE=$(ALT_BASE_IMAGE)")
	assertContains(t, container.Args, "--build-arg=ALT_BRANCH=$(ALT_BRANCH)")
	assertEnv(t, container.Env, "ALT_BRANCH", "p10")
	assertEnv(t, container.Env, "ALT_BASE_IMAGE", "registry.altlinux.org/alt/alt:p10")
	assertPolicyNameFieldRef(t, container.Env)

	assertVolume(t, podSpec.Volumes, buildContextVolumeName, func(volume corev1.Volume) {
		if volume.ConfigMap == nil || volume.ConfigMap.Name != "demo-app-context" {
			t.Fatalf("build context volume = %#v, want ConfigMap demo-app-context", volume)
		}
	})
	assertVolume(t, podSpec.Volumes, buildResultsVolumeName, func(volume corev1.Volume) {
		if volume.EmptyDir == nil {
			t.Fatalf("results volume = %#v, want emptyDir", volume)
		}
	})
	assertMount(t, container.VolumeMounts, buildContextVolumeName, WorkspaceMountPath, true)
	assertMount(t, container.VolumeMounts, buildResultsVolumeName, ResultsMountPath, false)
}

func TestNewBuildJobUsesDefaultsWhenOptionalFieldsAreUnset(t *testing.T) {
	policy := testBuildPolicy()
	policy.Spec.Build.BuilderImage = ""

	job, err := NewBuildJob(policy, testBuildJobOptions())
	if err != nil {
		t.Fatalf("NewBuildJob() error = %v", err)
	}

	container := onlyBuildContainer(t, job)
	if container.Image != DefaultKanikoExecutorImage {
		t.Fatalf("default builder image = %q, want %q", container.Image, DefaultKanikoExecutorImage)
	}
	if job.Spec.Template.Spec.ServiceAccountName != DefaultJobServiceAccountName {
		t.Fatalf("default serviceAccountName = %q, want %q", job.Spec.Template.Spec.ServiceAccountName, DefaultJobServiceAccountName)
	}
	if got := derefInt32(job.Spec.BackoffLimit); got != DefaultJobBackoffLimit {
		t.Fatalf("default backoffLimit = %d, want %d", got, DefaultJobBackoffLimit)
	}
	if got := derefInt32(job.Spec.TTLSecondsAfterFinished); got != DefaultJobTTLSeconds {
		t.Fatalf("default ttlSecondsAfterFinished = %d, want %d", got, DefaultJobTTLSeconds)
	}
	if got := derefInt64(job.Spec.ActiveDeadlineSeconds); got != DefaultJobActiveDeadlineSeconds {
		t.Fatalf("default activeDeadlineSeconds = %d, want %d", got, DefaultJobActiveDeadlineSeconds)
	}

	assertNoVolume(t, job.Spec.Template.Spec.Volumes, dockerConfigVolumeName)
	assertNoMount(t, container.VolumeMounts, dockerConfigVolumeName)
}

func TestNewBuildJobAppliesTemplateOverridesAndRegistrySecretMount(t *testing.T) {
	policy := testBuildPolicy()
	ttl := int32(300)
	backoff := int32(2)
	deadline := int64(1200)
	policy.Spec.JobTemplate.ServiceAccountName = "image-builder"
	policy.Spec.JobTemplate.TTLSecondsAfterFinished = &ttl
	policy.Spec.JobTemplate.BackoffLimit = &backoff
	policy.Spec.JobTemplate.ActiveDeadlineSeconds = &deadline
	policy.Spec.Build.RegistrySecretRef = &corev1.LocalObjectReference{Name: "registry-auth"}

	job, err := NewBuildJob(policy, testBuildJobOptions())
	if err != nil {
		t.Fatalf("NewBuildJob() error = %v", err)
	}

	if job.Spec.Template.Spec.ServiceAccountName != "image-builder" {
		t.Fatalf("serviceAccountName = %q, want image-builder", job.Spec.Template.Spec.ServiceAccountName)
	}
	if got := derefInt32(job.Spec.TTLSecondsAfterFinished); got != ttl {
		t.Fatalf("ttlSecondsAfterFinished = %d, want %d", got, ttl)
	}
	if got := derefInt32(job.Spec.BackoffLimit); got != backoff {
		t.Fatalf("backoffLimit = %d, want %d", got, backoff)
	}
	if got := derefInt64(job.Spec.ActiveDeadlineSeconds); got != deadline {
		t.Fatalf("activeDeadlineSeconds = %d, want %d", got, deadline)
	}

	assertVolume(t, job.Spec.Template.Spec.Volumes, dockerConfigVolumeName, func(volume corev1.Volume) {
		if volume.Secret == nil || volume.Secret.SecretName != "registry-auth" {
			t.Fatalf("docker config volume = %#v, want Secret registry-auth", volume)
		}
		if len(volume.Secret.Items) != 1 {
			t.Fatalf("secret items len = %d, want 1", len(volume.Secret.Items))
		}
		item := volume.Secret.Items[0]
		if item.Key != corev1.DockerConfigJsonKey || item.Path != "config.json" {
			t.Fatalf("secret item = %#v, want .dockerconfigjson -> config.json", item)
		}
	})
	assertMount(t, onlyBuildContainer(t, job).VolumeMounts, dockerConfigVolumeName, DockerConfigMountPath, true)
}

func TestNewBuildJobSetsConservativeResourceDefaults(t *testing.T) {
	job, err := NewBuildJob(testBuildPolicy(), testBuildJobOptions())
	if err != nil {
		t.Fatalf("NewBuildJob() error = %v", err)
	}

	resources := onlyBuildContainer(t, job).Resources
	assertQuantity(t, resources.Requests[corev1.ResourceCPU], "250m")
	assertQuantity(t, resources.Requests[corev1.ResourceMemory], "512Mi")
	assertQuantity(t, resources.Limits[corev1.ResourceCPU], "2")
	assertQuantity(t, resources.Limits[corev1.ResourceMemory], "2Gi")
}

func TestNewBuildJobDoesNotUseFakeSuccessPath(t *testing.T) {
	job, err := NewBuildJob(testBuildPolicy(), testBuildJobOptions())
	if err != nil {
		t.Fatalf("NewBuildJob() error = %v", err)
	}

	container := onlyBuildContainer(t, job)
	if len(container.Command) != 0 {
		t.Fatalf("container command = %v, want kaniko image entrypoint", container.Command)
	}
	for _, arg := range container.Args {
		if strings.Contains(arg, "echo") || strings.Contains(arg, "true") {
			t.Fatalf("kaniko arg %q looks like a fake success path", arg)
		}
	}
}

func TestNewBuildJobRejectsInvalidInputs(t *testing.T) {
	policy := testBuildPolicy()
	tests := []struct {
		name    string
		policy  *securityv1alpha1.AltImageUpdatePolicy
		opts    BuildJobOptions
		wantErr string
	}{
		{
			name:    "nil policy",
			policy:  nil,
			opts:    testBuildJobOptions(),
			wantErr: "policy is nil",
		},
		{
			name: "unsupported builder",
			policy: func() *securityv1alpha1.AltImageUpdatePolicy {
				copy := policy.DeepCopy()
				copy.Spec.Build.Builder = "BuildKit"
				return copy
			}(),
			opts:    testBuildJobOptions(),
			wantErr: "unsupported build builder",
		},
		{
			name: "unsupported context type",
			policy: func() *securityv1alpha1.AltImageUpdatePolicy {
				copy := policy.DeepCopy()
				copy.Spec.Build.Context.Type = "Git"
				return copy
			}(),
			opts:    testBuildJobOptions(),
			wantErr: "unsupported build context type",
		},
		{
			name:    "empty run key",
			policy:  policy,
			opts:    BuildJobOptions{BuildID: buildJobBuildID, BuiltImage: buildJobBuiltImage},
			wantErr: "run key is required",
		},
		{
			name:    "empty build id",
			policy:  policy,
			opts:    BuildJobOptions{RunKey: buildJobRunKey, BuiltImage: buildJobBuiltImage},
			wantErr: "build id is required",
		},
		{
			name:    "empty destination",
			policy:  policy,
			opts:    BuildJobOptions{RunKey: buildJobRunKey, BuildID: buildJobBuildID},
			wantErr: "destination image is required",
		},
		{
			name: "empty registry secret",
			policy: func() *securityv1alpha1.AltImageUpdatePolicy {
				copy := policy.DeepCopy()
				copy.Spec.Build.RegistrySecretRef = &corev1.LocalObjectReference{}
				return copy
			}(),
			opts:    testBuildJobOptions(),
			wantErr: "registry secret name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewBuildJob(tt.policy, tt.opts)
			if err == nil {
				t.Fatalf("NewBuildJob() error = nil, want containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewBuildJob() error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func testBuildPolicy() *securityv1alpha1.AltImageUpdatePolicy {
	return &securityv1alpha1.AltImageUpdatePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-app-policy",
			Namespace: "alt-image-update-demo",
			UID:       types.UID("11111111-2222-3333-4444-555555555555"),
		},
		Spec: securityv1alpha1.AltImageUpdatePolicySpec{
			Alt: securityv1alpha1.AltSpec{
				Branch:    securityv1alpha1.AltBranchP10,
				BaseImage: "registry.altlinux.org/alt/alt:p10",
			},
			Build: securityv1alpha1.BuildSpec{
				Builder:      securityv1alpha1.BuilderTypeKaniko,
				BuilderImage: "gcr.io/kaniko-project/executor:v1.23.2",
				Context: securityv1alpha1.BuildContextSpec{
					Type: securityv1alpha1.BuildContextTypeConfigMap,
					ConfigMapRef: securityv1alpha1.ConfigMapBuildContextRef{
						Name:          "demo-app-context",
						DockerfileKey: "Dockerfile",
					},
				},
			},
		},
	}
}

func testBuildJobOptions() BuildJobOptions {
	return BuildJobOptions{
		RunKey:     buildJobRunKey,
		BuildID:    buildJobBuildID,
		BuiltImage: buildJobBuiltImage,
	}
}

func onlyBuildContainer(t *testing.T, job *batchv1.Job) corev1.Container {
	t.Helper()

	if len(job.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("containers len = %d, want 1", len(job.Spec.Template.Spec.Containers))
	}
	container := job.Spec.Template.Spec.Containers[0]
	if container.Name != BuildContainerName {
		t.Fatalf("container name = %q, want %q", container.Name, BuildContainerName)
	}
	return container
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%q not found in %v", want, values)
}

func assertEnv(t *testing.T, env []corev1.EnvVar, name, want string) {
	t.Helper()
	for _, item := range env {
		if item.Name == name {
			if item.Value != want {
				t.Fatalf("env %s = %q, want %q", name, item.Value, want)
			}
			return
		}
	}
	t.Fatalf("env %s not found in %#v", name, env)
}

func assertPolicyNameFieldRef(t *testing.T, env []corev1.EnvVar) {
	t.Helper()
	for _, item := range env {
		if item.Name != "POLICY_NAME" {
			continue
		}
		if item.ValueFrom == nil || item.ValueFrom.FieldRef == nil {
			t.Fatalf("POLICY_NAME env = %#v, want fieldRef", item)
		}
		want := "metadata.labels['" + LabelPolicyName + "']"
		if item.ValueFrom.FieldRef.FieldPath != want {
			t.Fatalf("POLICY_NAME fieldPath = %q, want %q", item.ValueFrom.FieldRef.FieldPath, want)
		}
		return
	}
	t.Fatalf("POLICY_NAME env not found in %#v", env)
}

func assertVolume(t *testing.T, volumes []corev1.Volume, name string, check func(corev1.Volume)) {
	t.Helper()
	for _, volume := range volumes {
		if volume.Name == name {
			check(volume)
			return
		}
	}
	t.Fatalf("volume %q not found in %#v", name, volumes)
}

func assertNoVolume(t *testing.T, volumes []corev1.Volume, name string) {
	t.Helper()
	for _, volume := range volumes {
		if volume.Name == name {
			t.Fatalf("volume %q unexpectedly present: %#v", name, volume)
		}
	}
}

func assertMount(t *testing.T, mounts []corev1.VolumeMount, name, path string, readOnly bool) {
	t.Helper()
	for _, mount := range mounts {
		if mount.Name == name {
			if mount.MountPath != path || mount.ReadOnly != readOnly {
				t.Fatalf("mount %q = %#v, want path %q readOnly %v", name, mount, path, readOnly)
			}
			return
		}
	}
	t.Fatalf("mount %q not found in %#v", name, mounts)
}

func assertNoMount(t *testing.T, mounts []corev1.VolumeMount, name string) {
	t.Helper()
	for _, mount := range mounts {
		if mount.Name == name {
			t.Fatalf("mount %q unexpectedly present: %#v", name, mount)
		}
	}
}

func assertQuantity(t *testing.T, got resource.Quantity, want string) {
	t.Helper()
	wantQuantity := resource.MustParse(want)
	if got.Cmp(wantQuantity) != 0 {
		t.Fatalf("quantity = %s, want %s", got.String(), wantQuantity.String())
	}
}

func derefInt32(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
