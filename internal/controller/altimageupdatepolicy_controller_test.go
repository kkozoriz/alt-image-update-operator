package controller

import (
	"context"
	"testing"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
	"alt-image-update-operator/internal/run"
	appsv1 "k8s.io/api/apps/v1"
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

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
