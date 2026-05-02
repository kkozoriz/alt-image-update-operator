package jobs

import (
	"strings"
	"testing"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	checkJobRunKey  = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	checkJobBuildID = "g7-bbbbbbbbbbbbbbbb"
)

func TestNewCheckJobCreatesRealAltAptSimulationJob(t *testing.T) {
	policy := testCheckPolicy()

	job, err := NewCheckJob(policy, testCheckJobOptions())
	if err != nil {
		t.Fatalf("NewCheckJob() error = %v", err)
	}

	if job.Name != CheckJobName(policy.Name, checkJobBuildID) {
		t.Fatalf("job name = %q, want %q", job.Name, CheckJobName(policy.Name, checkJobBuildID))
	}
	if job.Namespace != policy.Namespace {
		t.Fatalf("job namespace = %q, want %q", job.Namespace, policy.Namespace)
	}
	if job.Labels[LabelJobType] != string(JobTypeCheck) {
		t.Fatalf("job type label = %q, want check", job.Labels[LabelJobType])
	}
	if job.Annotations[AnnotationRunKey] != checkJobRunKey {
		t.Fatalf("run key annotation = %q, want %q", job.Annotations[AnnotationRunKey], checkJobRunKey)
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
	if len(podSpec.Volumes) != 0 {
		t.Fatalf("volumes = %#v, want none", podSpec.Volumes)
	}
	container := onlyCheckContainer(t, job)
	if container.Image != policy.Spec.Alt.BaseImage {
		t.Fatalf("check image = %q, want %q", container.Image, policy.Spec.Alt.BaseImage)
	}
	if strings.Join(container.Command, " ") != "/bin/sh -ec" {
		t.Fatalf("command = %v, want /bin/sh -ec", container.Command)
	}
	if len(container.Args) != 1 {
		t.Fatalf("args len = %d, want 1: %v", len(container.Args), container.Args)
	}
	if !strings.Contains(container.Args[0], "apt-get update") {
		t.Fatalf("args = %v, want apt-get update", container.Args)
	}
	if !strings.Contains(container.Args[0], "apt-get -s dist-upgrade") {
		t.Fatalf("args = %v, want apt-get -s dist-upgrade", container.Args)
	}
	assertEnv(t, container.Env, "ALT_BRANCH", "p10")
	assertPolicyNameFieldRef(t, container.Env)
}

func TestNewCheckJobAppliesTemplateDefaultsAndOverrides(t *testing.T) {
	policy := testCheckPolicy()

	job, err := NewCheckJob(policy, testCheckJobOptions())
	if err != nil {
		t.Fatalf("NewCheckJob() error = %v", err)
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

	ttl := int32(120)
	backoff := int32(1)
	deadline := int64(600)
	policy.Spec.JobTemplate.ServiceAccountName = "alt-checker"
	policy.Spec.JobTemplate.TTLSecondsAfterFinished = &ttl
	policy.Spec.JobTemplate.BackoffLimit = &backoff
	policy.Spec.JobTemplate.ActiveDeadlineSeconds = &deadline

	job, err = NewCheckJob(policy, testCheckJobOptions())
	if err != nil {
		t.Fatalf("NewCheckJob() with overrides error = %v", err)
	}
	if job.Spec.Template.Spec.ServiceAccountName != "alt-checker" {
		t.Fatalf("serviceAccountName = %q, want alt-checker", job.Spec.Template.Spec.ServiceAccountName)
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
}

func TestNewCheckJobSetsConservativeSecurityContext(t *testing.T) {
	job, err := NewCheckJob(testCheckPolicy(), testCheckJobOptions())
	if err != nil {
		t.Fatalf("NewCheckJob() error = %v", err)
	}

	podSecurityContext := job.Spec.Template.Spec.SecurityContext
	if podSecurityContext == nil || podSecurityContext.SeccompProfile == nil || podSecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("pod securityContext = %#v, want RuntimeDefault seccomp profile", podSecurityContext)
	}

	securityContext := onlyCheckContainer(t, job).SecurityContext
	if securityContext == nil {
		t.Fatalf("container securityContext = nil")
	}
	if securityContext.AllowPrivilegeEscalation == nil || *securityContext.AllowPrivilegeEscalation {
		t.Fatalf("allowPrivilegeEscalation = %v, want false", securityContext.AllowPrivilegeEscalation)
	}
	if securityContext.Privileged == nil || *securityContext.Privileged {
		t.Fatalf("privileged = %v, want false", securityContext.Privileged)
	}
	if securityContext.ReadOnlyRootFilesystem == nil || *securityContext.ReadOnlyRootFilesystem {
		t.Fatalf("readOnlyRootFilesystem = %v, want false so apt can write package lists", securityContext.ReadOnlyRootFilesystem)
	}
	if securityContext.Capabilities == nil || len(securityContext.Capabilities.Drop) != 1 || securityContext.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("capabilities = %#v, want drop ALL", securityContext.Capabilities)
	}
}

func TestNewCheckJobSetsResourceDefaults(t *testing.T) {
	job, err := NewCheckJob(testCheckPolicy(), testCheckJobOptions())
	if err != nil {
		t.Fatalf("NewCheckJob() error = %v", err)
	}

	resources := onlyCheckContainer(t, job).Resources
	assertQuantity(t, resources.Requests[corev1.ResourceCPU], "100m")
	assertQuantity(t, resources.Requests[corev1.ResourceMemory], "128Mi")
	assertQuantity(t, resources.Limits[corev1.ResourceCPU], "1")
	assertQuantity(t, resources.Limits[corev1.ResourceMemory], "512Mi")
}

func TestNewCheckJobDoesNotUseFakeSuccessPath(t *testing.T) {
	job, err := NewCheckJob(testCheckPolicy(), testCheckJobOptions())
	if err != nil {
		t.Fatalf("NewCheckJob() error = %v", err)
	}

	arg := onlyCheckContainer(t, job).Args[0]
	if strings.Contains(arg, "echo") || strings.Contains(arg, " true") || strings.Contains(arg, "exit 0") {
		t.Fatalf("check command %q looks like a fake success path", arg)
	}
}

func TestNewCheckJobRejectsInvalidInputs(t *testing.T) {
	policy := testCheckPolicy()
	tests := []struct {
		name    string
		policy  *securityv1alpha1.AltImageUpdatePolicy
		opts    CheckJobOptions
		wantErr string
	}{
		{
			name:    "nil policy",
			policy:  nil,
			opts:    testCheckJobOptions(),
			wantErr: "policy is nil",
		},
		{
			name: "unsupported check mode",
			policy: func() *securityv1alpha1.AltImageUpdatePolicy {
				copy := policy.DeepCopy()
				copy.Spec.Check.Mode = securityv1alpha1.CheckModeAlways
				return copy
			}(),
			opts:    testCheckJobOptions(),
			wantErr: "unsupported check mode",
		},
		{
			name:    "empty run key",
			policy:  policy,
			opts:    CheckJobOptions{BuildID: checkJobBuildID},
			wantErr: "run key is required",
		},
		{
			name:    "empty build id",
			policy:  policy,
			opts:    CheckJobOptions{RunKey: checkJobRunKey},
			wantErr: "build id is required",
		},
		{
			name: "empty base image",
			policy: func() *securityv1alpha1.AltImageUpdatePolicy {
				copy := policy.DeepCopy()
				copy.Spec.Alt.BaseImage = " "
				return copy
			}(),
			opts:    testCheckJobOptions(),
			wantErr: "ALT base image is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCheckJob(tt.policy, tt.opts)
			if err == nil {
				t.Fatalf("NewCheckJob() error = nil, want containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewCheckJob() error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func testCheckPolicy() *securityv1alpha1.AltImageUpdatePolicy {
	return &securityv1alpha1.AltImageUpdatePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-app-policy",
			Namespace: "alt-image-update-demo",
			UID:       types.UID("66666666-7777-8888-9999-000000000000"),
		},
		Spec: securityv1alpha1.AltImageUpdatePolicySpec{
			Alt: securityv1alpha1.AltSpec{
				Branch:    securityv1alpha1.AltBranchP10,
				BaseImage: "registry.altlinux.org/alt/alt:p10",
			},
			Check: securityv1alpha1.CheckSpec{
				Mode: securityv1alpha1.CheckModeAltAptSimulation,
			},
		},
	}
}

func testCheckJobOptions() CheckJobOptions {
	return CheckJobOptions{
		RunKey:  checkJobRunKey,
		BuildID: checkJobBuildID,
	}
}

func onlyCheckContainer(t *testing.T, job *batchv1.Job) corev1.Container {
	t.Helper()

	if len(job.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("containers len = %d, want 1", len(job.Spec.Template.Spec.Containers))
	}
	container := job.Spec.Template.Spec.Containers[0]
	if container.Name != CheckContainerName {
		t.Fatalf("container name = %q, want %q", container.Name, CheckContainerName)
	}
	return container
}
