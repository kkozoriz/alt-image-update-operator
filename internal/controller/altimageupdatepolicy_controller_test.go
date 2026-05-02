package controller

import (
	"context"
	"testing"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
	operatorimage "alt-image-update-operator/internal/image"
	"alt-image-update-operator/internal/jobs"
	"alt-image-update-operator/internal/run"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNamespace = "default"

func TestReconcilePreflightSuccessUpdatesObservedGenerationAndRunIdentity(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
	)

	if _, err := reconciler.Reconcile(ctx, requestFor(policy)); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.ObservedGeneration != policy.Generation {
		t.Fatalf("ObservedGeneration = %d, want %d", updated.Status.ObservedGeneration, policy.Generation)
	}
	if updated.Status.CurrentRunKey != run.RunKey(policy) {
		t.Fatalf("CurrentRunKey = %q, want %q", updated.Status.CurrentRunKey, run.RunKey(policy))
	}
	if updated.Status.BuildID != run.BuildID(policy) {
		t.Fatalf("BuildID = %q, want %q", updated.Status.BuildID, run.BuildID(policy))
	}
	if updated.Status.Phase != securityv1alpha1.PolicyPhasePending {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhasePending)
	}
	if updated.Status.Reason != reasonPreflightSucceeded {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, reasonPreflightSucceeded)
	}
	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil {
		t.Fatalf("missing %s condition", securityv1alpha1.ConditionFailed)
	}
	if failed.Status != metav1.ConditionFalse {
		t.Fatalf("Failed condition status = %q, want %q", failed.Status, metav1.ConditionFalse)
	}
}

func TestReconcileAlwaysCreatesOneBuildJobAndBuildingStatus(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
	)

	if _, err := reconciler.Reconcile(ctx, requestFor(policy)); err != nil {
		t.Fatalf("first Reconcile returned error: %v", err)
	}
	if _, err := reconciler.Reconcile(ctx, requestFor(policy)); err != nil {
		t.Fatalf("second Reconcile returned error: %v", err)
	}

	buildJobs := listJobs(t, ctx, reconciler.Client)
	if len(buildJobs.Items) != 1 {
		t.Fatalf("build Jobs len = %d, want 1", len(buildJobs.Items))
	}

	buildJob := buildJobs.Items[0]
	buildID := run.BuildID(policy)
	runKey := run.RunKey(policy)
	wantJobName := jobs.BuildJobName(policy.Name, buildID)
	if buildJob.Name != wantJobName {
		t.Fatalf("build Job name = %q, want %q", buildJob.Name, wantJobName)
	}
	if buildJob.Labels[jobs.LabelJobType] != string(jobs.JobTypeBuild) {
		t.Fatalf("job type label = %q, want %q", buildJob.Labels[jobs.LabelJobType], jobs.JobTypeBuild)
	}
	if buildJob.Labels[jobs.LabelManagedBy] == "" || buildJob.Labels[jobs.LabelRunKey] == "" || buildJob.Labels[jobs.LabelBuildID] == "" {
		t.Fatalf("build Job labels are missing controller lookup labels: %#v", buildJob.Labels)
	}
	if buildJob.Annotations[jobs.AnnotationRunKey] != runKey {
		t.Fatalf("run key annotation = %q, want %q", buildJob.Annotations[jobs.AnnotationRunKey], runKey)
	}
	if len(buildJob.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences len = %d, want 1", len(buildJob.OwnerReferences))
	}
	owner := buildJob.OwnerReferences[0]
	if owner.APIVersion != securityv1alpha1.GroupVersion.String() || owner.Kind != "AltImageUpdatePolicy" || owner.Name != policy.Name || owner.UID != policy.UID {
		t.Fatalf("ownerReference = %#v, want policy owner", owner)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Fatalf("ownerReference controller = %v, want true", owner.Controller)
	}

	wantImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}
	if len(buildJob.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("build Job containers len = %d, want 1", len(buildJob.Spec.Template.Spec.Containers))
	}
	if !containsString(buildJob.Spec.Template.Spec.Containers[0].Args, "--destination="+wantImage) {
		t.Fatalf("build Job args = %v, want destination %q", buildJob.Spec.Template.Spec.Containers[0].Args, wantImage)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseBuilding {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseBuilding)
	}
	if updated.Status.LastBuildJobName != wantJobName {
		t.Fatalf("LastBuildJobName = %q, want %q", updated.Status.LastBuildJobName, wantJobName)
	}
	if updated.Status.LastBuildStartTime == nil {
		t.Fatalf("LastBuildStartTime is nil, want build start timestamp")
	}
}

func TestReconcileMissingTargetDeploymentSetsFailedStatus(t *testing.T) {
	policy := testPolicy()
	reconciler := newTestReconciler(t, policy)

	assertFailedPreflight(t, reconciler, policy, reasonTargetDeploymentNotFound)
}

func TestReconcileMissingTargetContainerSetsFailedStatus(t *testing.T) {
	policy := testPolicy()
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "sidecar"),
	)

	assertFailedPreflight(t, reconciler, policy, reasonTargetContainerNotFound)
}

func TestReconcileMissingBuildContextConfigMapSetsFailedStatus(t *testing.T) {
	policy := testPolicy()
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
	)

	assertFailedPreflight(t, reconciler, policy, reasonBuildContextNotFound)
}

func TestReconcileMissingDockerfileKeySetsFailedStatus(t *testing.T) {
	policy := testPolicy()
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Containerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
	)

	assertFailedPreflight(t, reconciler, policy, reasonDockerfileKeyNotFound)
}

func assertFailedPreflight(t *testing.T, reconciler *AltImageUpdatePolicyReconciler, policy *securityv1alpha1.AltImageUpdatePolicy, wantReason string) {
	t.Helper()

	ctx := context.Background()
	if _, err := reconciler.Reconcile(ctx, requestFor(policy)); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.ObservedGeneration != policy.Generation {
		t.Fatalf("ObservedGeneration = %d, want %d", updated.Status.ObservedGeneration, policy.Generation)
	}
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseFailed {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseFailed)
	}
	if updated.Status.Reason != wantReason {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, wantReason)
	}
	if updated.Status.CurrentRunKey != run.RunKey(policy) {
		t.Fatalf("CurrentRunKey = %q, want %q", updated.Status.CurrentRunKey, run.RunKey(policy))
	}
	if updated.Status.BuildID != run.BuildID(policy) {
		t.Fatalf("BuildID = %q, want %q", updated.Status.BuildID, run.BuildID(policy))
	}

	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil {
		t.Fatalf("missing %s condition", securityv1alpha1.ConditionFailed)
	}
	if failed.Status != metav1.ConditionTrue {
		t.Fatalf("Failed condition status = %q, want %q", failed.Status, metav1.ConditionTrue)
	}
	if failed.Reason != wantReason {
		t.Fatalf("Failed condition reason = %q, want %q", failed.Reason, wantReason)
	}
}

func newTestReconciler(t *testing.T, objects ...client.Object) *AltImageUpdatePolicyReconciler {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := securityv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add security scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add batch scheme: %v", err)
	}

	return &AltImageUpdatePolicyReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objects...).
			WithStatusSubresource(&securityv1alpha1.AltImageUpdatePolicy{}).
			Build(),
		Scheme: scheme,
	}
}

func testPolicy() *securityv1alpha1.AltImageUpdatePolicy {
	return &securityv1alpha1.AltImageUpdatePolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: securityv1alpha1.GroupVersion.String(),
			Kind:       "AltImageUpdatePolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "demo-policy",
			Namespace:  testNamespace,
			Generation: 3,
			UID:        "policy-uid",
		},
		Spec: securityv1alpha1.AltImageUpdatePolicySpec{
			Alt: securityv1alpha1.AltSpec{
				Branch:    securityv1alpha1.AltBranchP10,
				BaseImage: "registry.altlinux.org/alt/alt:p10",
			},
			Check: securityv1alpha1.CheckSpec{
				Mode: securityv1alpha1.CheckModeAlways,
			},
			Build: securityv1alpha1.BuildSpec{
				Builder:     securityv1alpha1.BuilderTypeKaniko,
				OutputImage: "localhost:5001/alt/demo-app",
				Context: securityv1alpha1.BuildContextSpec{
					Type: securityv1alpha1.BuildContextTypeConfigMap,
					ConfigMapRef: securityv1alpha1.ConfigMapBuildContextRef{
						Name:          "demo-context",
						DockerfileKey: "Dockerfile",
					},
				},
			},
			TargetRef: securityv1alpha1.TargetReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "demo-app",
			},
			ContainerName: "app",
		},
	}
}

func testDeployment(name string, containerNames ...string) *appsv1.Deployment {
	containers := make([]corev1.Container, 0, len(containerNames))
	for _, containerName := range containerNames {
		containers = append(containers, corev1.Container{
			Name:  containerName,
			Image: "registry.altlinux.org/alt/alt:p10",
		})
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: containers,
				},
			},
		},
	}
}

func testConfigMap(name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Data: data,
	}
}

func requestFor(policy *securityv1alpha1.AltImageUpdatePolicy) ctrl.Request {
	return ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: policy.Namespace,
			Name:      policy.Name,
		},
	}
}

func getPolicy(t *testing.T, ctx context.Context, c client.Client, name string) *securityv1alpha1.AltImageUpdatePolicy {
	t.Helper()

	var policy securityv1alpha1.AltImageUpdatePolicy
	if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, &policy); err != nil {
		t.Fatalf("get policy: %v", err)
	}
	return &policy
}

func listJobs(t *testing.T, ctx context.Context, c client.Client) *batchv1.JobList {
	t.Helper()

	var jobList batchv1.JobList
	if err := c.List(ctx, &jobList, client.InNamespace(testNamespace)); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	return &jobList
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
