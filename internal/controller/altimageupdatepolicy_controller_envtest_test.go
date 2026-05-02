package controller

import (
	"context"
	"strconv"
	"testing"
	"time"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
	operatorimage "alt-image-update-operator/internal/image"
	"alt-image-update-operator/internal/jobs"
	"alt-image-update-operator/internal/run"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestEnvtestInstallsCRDAndUsesStatusSubresource(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)
	envtestHasAltImageUpdatePolicyCRD(t)
	envtestSchemeHasCoreTypes(t)

	namespace := createEnvtestNamespace(t, ctx, c)
	policy := envtestPolicy(namespace)
	policy.Status.Phase = securityv1alpha1.PolicyPhaseFailed
	if err := c.Create(ctx, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	storedPolicy := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if storedPolicy.Status.Phase != "" {
		t.Fatalf("status phase after create = %q, want empty status ignored by status subresource", storedPolicy.Status.Phase)
	}

	storedPolicy.Status.Phase = securityv1alpha1.PolicyPhasePending
	storedPolicy.Status.ObservedGeneration = storedPolicy.Generation
	storedPolicy.Status.Reason = "EnvtestStatusUpdate"
	if err := c.Status().Update(ctx, storedPolicy); err != nil {
		t.Fatalf("status update policy: %v", err)
	}

	updatedPolicy := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if updatedPolicy.Status.Phase != securityv1alpha1.PolicyPhasePending {
		t.Fatalf("status phase after status update = %q, want %q", updatedPolicy.Status.Phase, securityv1alpha1.PolicyPhasePending)
	}

	updatedPolicy.Status.Phase = securityv1alpha1.PolicyPhaseFailed
	updatedPolicy.Annotations = map[string]string{"envtest.security.altlinux.org/normal-update": "true"}
	if err := c.Update(ctx, updatedPolicy); err != nil {
		t.Fatalf("normal update policy: %v", err)
	}

	afterNormalUpdate := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if afterNormalUpdate.Status.Phase != securityv1alpha1.PolicyPhasePending {
		t.Fatalf("status phase after normal update = %q, want status subresource to preserve %q", afterNormalUpdate.Status.Phase, securityv1alpha1.PolicyPhasePending)
	}

	deployment := envtestDeployment(namespace, "demo-app", "app")
	if err := c.Create(ctx, deployment); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	configMap := testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"})
	configMap.Namespace = namespace
	if err := c.Create(ctx, configMap); err != nil {
		t.Fatalf("create configmap: %v", err)
	}
	job := envtestJob(namespace, "direct-build-job")
	if err := c.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	job.Status.Active = 1
	if err := c.Status().Update(ctx, job); err != nil {
		t.Fatalf("status update job: %v", err)
	}

	var jobList batchv1.JobList
	if err := c.List(ctx, &jobList, client.InNamespace(namespace)); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobList.Items) != 1 || jobList.Items[0].Status.Active != 1 {
		t.Fatalf("jobs after create/status update = %#v, want one active job", jobList.Items)
	}
}

func TestEnvtestReconcilerCreatesBuildJobAndObservesJobStatus(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)
	reconciler := newEnvtestReconciler(t)

	namespace := createEnvtestNamespace(t, ctx, c)
	policy := envtestPolicy(namespace)
	deployment := envtestDeployment(namespace, policy.Spec.TargetRef.Name, policy.Spec.ContainerName)
	configMap := testConfigMap(policy.Spec.Build.Context.ConfigMapRef.Name, map[string]string{
		policy.Spec.Build.Context.ConfigMapRef.DockerfileKey: "FROM registry.altlinux.org/alt/alt:p10\n",
	})
	configMap.Namespace = namespace

	for _, object := range []client.Object{deployment, configMap, policy} {
		if err := c.Create(ctx, object); err != nil {
			t.Fatalf("create %T: %v", object, err)
		}
	}
	policy = getEnvtestPolicy(t, ctx, c, namespace, policy.Name)

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: policy.Name}})
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if result.RequeueAfter != buildJobRequeueAfter {
		t.Fatalf("first reconcile result = %#v, want build job requeue", result)
	}

	afterFirstReconcile := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if afterFirstReconcile.Status.Phase != securityv1alpha1.PolicyPhaseBuilding {
		t.Fatalf("phase after first reconcile = %q, want %q", afterFirstReconcile.Status.Phase, securityv1alpha1.PolicyPhaseBuilding)
	}
	if afterFirstReconcile.Status.ObservedGeneration != policy.Generation {
		t.Fatalf("observedGeneration = %d, want %d", afterFirstReconcile.Status.ObservedGeneration, policy.Generation)
	}
	if afterFirstReconcile.Status.LastBuildJobName == "" {
		t.Fatalf("lastBuildJobName is empty, want reconciler-created Job")
	}

	var buildJob batchv1.Job
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: afterFirstReconcile.Status.LastBuildJobName}, &buildJob); err != nil {
		t.Fatalf("get build job: %v", err)
	}
	if buildJob.Labels[jobs.LabelJobType] != string(jobs.JobTypeBuild) {
		t.Fatalf("job type label = %q, want %q", buildJob.Labels[jobs.LabelJobType], jobs.JobTypeBuild)
	}

	buildJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	if err := c.Status().Update(ctx, &buildJob); err != nil {
		t.Fatalf("status update completed build job: %v", err)
	}

	result, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: policy.Name}})
	if err != nil {
		t.Fatalf("second reconcile after completed build job: %v", err)
	}
	if result.RequeueAfter != rolloutRequeueAfter {
		t.Fatalf("second reconcile result = %#v, want rollout requeue", result)
	}

	buildID := run.BuildID(policy)
	builtImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("build reference: %v", err)
	}

	updatedDeployment := getEnvtestDeployment(t, ctx, c, namespace, deployment.Name)
	if updatedDeployment.Spec.Template.Spec.Containers[0].Image != builtImage {
		t.Fatalf("deployment image = %q, want %q", updatedDeployment.Spec.Template.Spec.Containers[0].Image, builtImage)
	}

	afterSecondReconcile := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if afterSecondReconcile.Status.Phase != securityv1alpha1.PolicyPhaseRollingOut {
		t.Fatalf("phase after completed job reconcile = %q, want %q", afterSecondReconcile.Status.Phase, securityv1alpha1.PolicyPhaseRollingOut)
	}
	if afterSecondReconcile.Status.LastBuiltImage != builtImage {
		t.Fatalf("lastBuiltImage = %q, want %q", afterSecondReconcile.Status.LastBuiltImage, builtImage)
	}
	if afterSecondReconcile.Status.LastAppliedImage != builtImage {
		t.Fatalf("lastAppliedImage = %q, want %q", afterSecondReconcile.Status.LastAppliedImage, builtImage)
	}
}

func TestEnvtestRejectsInvalidCheckMode(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)

	namespace := createEnvtestNamespace(t, ctx, c)
	policy := envtestPolicy(namespace)
	policy.Name = "invalid-check-mode"
	policy.Spec.Check.Mode = securityv1alpha1.CheckMode("Sometimes")

	err := c.Create(ctx, policy)
	if err == nil {
		t.Fatalf("create policy with invalid check mode succeeded, want API server validation error")
	}
	if !apierrors.IsInvalid(err) {
		t.Fatalf("create policy with invalid check mode error = %v, want invalid error", err)
	}
}

func TestEnvtestRejectsMissingRequiredSpecFields(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)

	namespace := createEnvtestNamespace(t, ctx, c)
	tests := []struct {
		name   string
		object map[string]interface{}
	}{
		{
			name: "missing spec",
			object: map[string]interface{}{
				"apiVersion": securityv1alpha1.GroupVersion.String(),
				"kind":       "AltImageUpdatePolicy",
				"metadata": map[string]interface{}{
					"name":      "missing-spec",
					"namespace": namespace,
				},
			},
		},
		{
			name: "missing required spec children",
			object: map[string]interface{}{
				"apiVersion": securityv1alpha1.GroupVersion.String(),
				"kind":       "AltImageUpdatePolicy",
				"metadata": map[string]interface{}{
					"name":      "missing-spec-children",
					"namespace": namespace,
				},
				"spec": map[string]interface{}{
					"alt": map[string]interface{}{
						"branch":    string(securityv1alpha1.AltBranchP10),
						"baseImage": "registry.altlinux.org/alt/alt:p10",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := c.Create(ctx, &unstructured.Unstructured{Object: tt.object})
			if err == nil {
				t.Fatalf("create invalid policy succeeded, want API server validation error")
			}
			if !apierrors.IsInvalid(err) {
				t.Fatalf("create invalid policy error = %v, want invalid error", err)
			}
		})
	}
}

func TestEnvtestReconcilerSetsFailedStatusForMissingDeployment(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)
	reconciler := newEnvtestReconciler(t)

	namespace := createEnvtestNamespace(t, ctx, c)
	policy := envtestPolicy(namespace)
	if err := c.Create(ctx, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	assertEnvtestFailedPreflight(t, ctx, reconciler, namespace, policy.Name, reasonTargetDeploymentNotFound)
}

func TestEnvtestReconcilerSetsFailedStatusForMissingConfigMap(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)
	reconciler := newEnvtestReconciler(t)

	namespace := createEnvtestNamespace(t, ctx, c)
	policy := envtestPolicy(namespace)
	deployment := envtestDeployment(namespace, policy.Spec.TargetRef.Name, policy.Spec.ContainerName)

	for _, object := range []client.Object{deployment, policy} {
		if err := c.Create(ctx, object); err != nil {
			t.Fatalf("create %T: %v", object, err)
		}
	}

	assertEnvtestFailedPreflight(t, ctx, reconciler, namespace, policy.Name, reasonBuildContextNotFound)
}

func TestEnvtestReconcilerSetsFailedStatusForMissingDockerfileKey(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)
	reconciler := newEnvtestReconciler(t)

	namespace := createEnvtestNamespace(t, ctx, c)
	policy := envtestPolicy(namespace)
	deployment := envtestDeployment(namespace, policy.Spec.TargetRef.Name, policy.Spec.ContainerName)
	configMap := testConfigMap(policy.Spec.Build.Context.ConfigMapRef.Name, map[string]string{
		"Containerfile": "FROM registry.altlinux.org/alt/alt:p10\n",
	})
	configMap.Namespace = namespace

	for _, object := range []client.Object{deployment, configMap, policy} {
		if err := c.Create(ctx, object); err != nil {
			t.Fatalf("create %T: %v", object, err)
		}
	}

	assertEnvtestFailedPreflight(t, ctx, reconciler, namespace, policy.Name, reasonDockerfileKeyNotFound)
}

func envtestClientForTest(t *testing.T) client.Client {
	t.Helper()
	requireEnvtest(t)
	return envtestClient
}

func createEnvtestNamespace(t *testing.T, ctx context.Context, c client.Client) string {
	t.Helper()

	namespace := "envtest-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatalf("create namespace %q: %v", namespace, err)
	}
	t.Cleanup(func() {
		if err := c.Delete(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
			t.Logf("delete namespace %q: %v", namespace, err)
		}
	})
	return namespace
}

func envtestPolicy(namespace string) *securityv1alpha1.AltImageUpdatePolicy {
	policy := testPolicy()
	policy.ObjectMeta = metav1.ObjectMeta{
		Name:      "demo-policy",
		Namespace: namespace,
	}
	return policy
}

func envtestDeployment(namespace, name, containerName string) *appsv1.Deployment {
	replicas := int32(1)
	labels := map[string]string{"app": name}
	deployment := testDeployment(name, containerName)
	deployment.Namespace = namespace
	deployment.Labels = map[string]string{"app": name}
	deployment.Spec.Replicas = &replicas
	deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
	deployment.Spec.Template.Labels = labels
	return deployment
}

func envtestJob(namespace, name string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "build",
							Image:   "registry.altlinux.org/alt/alt:p10",
							Command: []string{"/bin/sh", "-ec", "true"},
						},
					},
				},
			},
		},
	}
}

func getEnvtestPolicy(t *testing.T, ctx context.Context, c client.Client, namespace, name string) *securityv1alpha1.AltImageUpdatePolicy {
	t.Helper()

	var policy securityv1alpha1.AltImageUpdatePolicy
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &policy); err != nil {
		t.Fatalf("get policy %s/%s: %v", namespace, name, err)
	}
	return &policy
}

func getEnvtestDeployment(t *testing.T, ctx context.Context, c client.Client, namespace, name string) *appsv1.Deployment {
	t.Helper()

	var deployment appsv1.Deployment
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &deployment); err != nil {
		t.Fatalf("get deployment %s/%s: %v", namespace, name, err)
	}
	return &deployment
}

func assertEnvtestFailedPreflight(t *testing.T, ctx context.Context, reconciler *AltImageUpdatePolicyReconciler, namespace, policyName, wantReason string) {
	t.Helper()

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: policyName}})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("Reconcile result = %#v, want no explicit requeue after failed preflight", result)
	}

	updated := getEnvtestPolicy(t, ctx, reconciler.Client, namespace, policyName)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseFailed {
		t.Fatalf("phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseFailed)
	}
	if updated.Status.Reason != wantReason {
		t.Fatalf("reason = %q, want %q", updated.Status.Reason, wantReason)
	}
	if updated.Status.ObservedGeneration != updated.Generation {
		t.Fatalf("observedGeneration = %d, want policy generation %d", updated.Status.ObservedGeneration, updated.Generation)
	}
	if updated.Status.CurrentRunKey != run.RunKey(updated) {
		t.Fatalf("currentRunKey = %q, want %q", updated.Status.CurrentRunKey, run.RunKey(updated))
	}
	if updated.Status.BuildID != run.BuildID(updated) {
		t.Fatalf("buildID = %q, want %q", updated.Status.BuildID, run.BuildID(updated))
	}

	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil {
		t.Fatalf("missing %s condition", securityv1alpha1.ConditionFailed)
	}
	if failed.Status != metav1.ConditionTrue {
		t.Fatalf("failed condition status = %q, want %q", failed.Status, metav1.ConditionTrue)
	}
	if failed.Reason != wantReason {
		t.Fatalf("failed condition reason = %q, want %q", failed.Reason, wantReason)
	}
}
