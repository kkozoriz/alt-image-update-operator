package controller

import (
	"context"
	"strconv"
	"testing"
	"time"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
	operatordeploy "alt-image-update-operator/internal/deploy"
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
	patchClient := &patchCountingClient{Client: c}
	reconciler.Client = patchClient

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
	assertEnvtestBuildJobCount(t, ctx, c, namespace, 1)

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
	if updatedDeployment.Spec.Template.Annotations[operatordeploy.AnnotationLastBuildID] != buildID {
		t.Fatalf("deployment buildID annotation = %q, want %q", updatedDeployment.Spec.Template.Annotations[operatordeploy.AnnotationLastBuildID], buildID)
	}
	if patchClient.patchCalls != 1 {
		t.Fatalf("deployment patch calls = %d, want exactly one patch after completed Build Job", patchClient.patchCalls)
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

	rolledOutDeployment := markEnvtestDeploymentRolledOut(t, ctx, c, namespace, deployment.Name)
	result, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: policy.Name}})
	if err != nil {
		t.Fatalf("third reconcile after Deployment rollout: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("third reconcile result = %#v, want terminal success without explicit requeue", result)
	}

	succeededPolicy := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if succeededPolicy.Status.Phase != securityv1alpha1.PolicyPhaseSucceeded {
		t.Fatalf("phase after rolled out deployment reconcile = %q, want %q", succeededPolicy.Status.Phase, securityv1alpha1.PolicyPhaseSucceeded)
	}
	if succeededPolicy.Status.LastRolloutTime == nil {
		t.Fatalf("lastRolloutTime is nil, want rollout completion timestamp")
	}
	ready := findCondition(succeededPolicy.Status.Conditions, securityv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("Ready condition = %#v, want True after rollout", ready)
	}
	rolloutCompleted := findCondition(succeededPolicy.Status.Conditions, securityv1alpha1.ConditionRolloutCompleted)
	if rolloutCompleted == nil || rolloutCompleted.Status != metav1.ConditionTrue {
		t.Fatalf("RolloutCompleted condition = %#v, want True after rollout", rolloutCompleted)
	}

	deploymentGenerationAfterSuccess := rolledOutDeployment.Generation
	policyResourceVersionAfterSuccess := succeededPolicy.ResourceVersion
	result, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: policy.Name}})
	if err != nil {
		t.Fatalf("fourth reconcile for terminal current run: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("fourth reconcile result = %#v, want no explicit requeue for terminal current run", result)
	}
	assertEnvtestBuildJobCount(t, ctx, c, namespace, 1)
	afterRepeatedReconcileDeployment := getEnvtestDeployment(t, ctx, c, namespace, deployment.Name)
	if afterRepeatedReconcileDeployment.Generation != deploymentGenerationAfterSuccess {
		t.Fatalf("deployment generation after repeated reconcile = %d, want unchanged %d", afterRepeatedReconcileDeployment.Generation, deploymentGenerationAfterSuccess)
	}
	if patchClient.patchCalls != 1 {
		t.Fatalf("deployment patch calls after repeated reconcile = %d, want still one", patchClient.patchCalls)
	}
	afterRepeatedReconcilePolicy := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if afterRepeatedReconcilePolicy.ResourceVersion != policyResourceVersionAfterSuccess {
		t.Fatalf("policy resourceVersion after repeated reconcile = %q, want unchanged %q", afterRepeatedReconcilePolicy.ResourceVersion, policyResourceVersionAfterSuccess)
	}

	retriggeredPolicy := afterRepeatedReconcilePolicy.DeepCopy()
	retriggeredPolicy.Spec.Trigger.ManualToken = "manual-retry-1"
	if err := c.Update(ctx, retriggeredPolicy); err != nil {
		t.Fatalf("update policy manualToken: %v", err)
	}
	retriggeredPolicy = getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	newBuildID := run.BuildID(retriggeredPolicy)
	if newBuildID == buildID {
		t.Fatalf("manualToken did not change buildID: %q", newBuildID)
	}

	result, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: policy.Name}})
	if err != nil {
		t.Fatalf("reconcile after manualToken change: %v", err)
	}
	if result.RequeueAfter != buildJobRequeueAfter {
		t.Fatalf("manualToken reconcile result = %#v, want build job requeue", result)
	}
	assertEnvtestBuildJobCount(t, ctx, c, namespace, 2)
	newBuildJobName := jobs.BuildJobName(policy.Name, newBuildID)
	var newBuildJob batchv1.Job
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: newBuildJobName}, &newBuildJob); err != nil {
		t.Fatalf("get retriggered build job %q: %v", newBuildJobName, err)
	}
	afterManualTokenReconcile := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if afterManualTokenReconcile.Status.Phase != securityv1alpha1.PolicyPhaseBuilding {
		t.Fatalf("phase after manualToken reconcile = %q, want %q", afterManualTokenReconcile.Status.Phase, securityv1alpha1.PolicyPhaseBuilding)
	}
	if afterManualTokenReconcile.Status.LastBuildJobName != newBuildJobName {
		t.Fatalf("lastBuildJobName after manualToken reconcile = %q, want %q", afterManualTokenReconcile.Status.LastBuildJobName, newBuildJobName)
	}
	if afterManualTokenReconcile.Status.LastAppliedImage != "" {
		t.Fatalf("lastAppliedImage after manualToken run reset = %q, want empty before new build completes", afterManualTokenReconcile.Status.LastAppliedImage)
	}
	if patchClient.patchCalls != 1 {
		t.Fatalf("deployment patch calls after manualToken build start = %d, want still one", patchClient.patchCalls)
	}
}

func TestEnvtestAltAptSimulationCreatesOneCheckJobAndDoesNotDuplicate(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)
	reconciler := newEnvtestReconciler(t)

	namespace := createEnvtestNamespace(t, ctx, c)
	policy := envtestAltAptSimulationPolicy(namespace)
	deployment := envtestDeployment(namespace, policy.Spec.TargetRef.Name, policy.Spec.ContainerName)
	configMap := envtestBuildContextConfigMap(namespace, policy)

	for _, object := range []client.Object{deployment, configMap, policy} {
		if err := c.Create(ctx, object); err != nil {
			t.Fatalf("create %T: %v", object, err)
		}
	}
	policy = getEnvtestPolicy(t, ctx, c, namespace, policy.Name)

	for i := 0; i < 2; i++ {
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: policy.Name}})
		if err != nil {
			t.Fatalf("reconcile %d: %v", i+1, err)
		}
		if result.RequeueAfter != checkJobRequeueAfter {
			t.Fatalf("reconcile %d result = %#v, want check job requeue", i+1, result)
		}
	}

	assertEnvtestCheckJobCount(t, ctx, c, namespace, 1)
	assertEnvtestBuildJobCount(t, ctx, c, namespace, 0)

	updated := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	wantCheckJobName := jobs.CheckJobName(policy.Name, run.BuildID(policy))
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseChecking {
		t.Fatalf("phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseChecking)
	}
	if updated.Status.LastCheckJobName != wantCheckJobName {
		t.Fatalf("lastCheckJobName = %q, want %q", updated.Status.LastCheckJobName, wantCheckJobName)
	}
	if updated.Status.LastBuildJobName != "" {
		t.Fatalf("lastBuildJobName = %q, want empty before check result", updated.Status.LastBuildJobName)
	}
}

func TestEnvtestAltAptSimulationNoUpdatesSetsUpToDateWithoutBuildJob(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)
	reconciler := newEnvtestReconciler(t)
	logReader := &fakeCheckJobLogReader{
		logs: "Reading Package Lists...\n0 upgraded, 0 newly installed, 0 removed and 0 not upgraded.\n",
	}
	reconciler.CheckLogReader = logReader

	namespace := createEnvtestNamespace(t, ctx, c)
	policy := createEnvtestAltAptSimulationInputs(t, ctx, c, namespace)
	checkJob := createEnvtestCompletedCheckJobForPolicy(t, ctx, c, policy)
	checkPod := createEnvtestCheckPodForJob(t, ctx, c, checkJob, "check-pod-no-updates")

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: policy.Name}})
	if err != nil {
		t.Fatalf("reconcile completed no-updates Check Job: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want terminal UpToDate without explicit requeue", result)
	}
	if logReader.calls != 1 {
		t.Fatalf("log reader calls = %d, want 1", logReader.calls)
	}
	if logReader.lastPodName != checkPod.Name {
		t.Fatalf("log reader pod = %q, want %q", logReader.lastPodName, checkPod.Name)
	}

	updated := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseUpToDate {
		t.Fatalf("phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseUpToDate)
	}
	if updated.Status.Reason != reasonNoUpdates {
		t.Fatalf("reason = %q, want %q", updated.Status.Reason, reasonNoUpdates)
	}
	if updated.Status.LastCheckJobName != checkJob.Name {
		t.Fatalf("lastCheckJobName = %q, want %q", updated.Status.LastCheckJobName, checkJob.Name)
	}
	if updated.Status.LastCheckTime == nil {
		t.Fatalf("lastCheckTime is nil, want completed check timestamp")
	}
	if updated.Status.LastBuildJobName != "" {
		t.Fatalf("lastBuildJobName = %q, want empty when no updates are available", updated.Status.LastBuildJobName)
	}
	assertEnvtestCheckJobCount(t, ctx, c, namespace, 1)
	assertEnvtestBuildJobCount(t, ctx, c, namespace, 0)

	checkCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionCheckCompleted)
	if checkCompleted == nil || checkCompleted.Status != metav1.ConditionTrue {
		t.Fatalf("CheckCompleted condition = %#v, want True", checkCompleted)
	}
	upToDate := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionUpToDate)
	if upToDate == nil || upToDate.Status != metav1.ConditionTrue {
		t.Fatalf("UpToDate condition = %#v, want True", upToDate)
	}
}

func TestEnvtestAltAptSimulationUpdatesAvailableCreatesBuildJob(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)
	reconciler := newEnvtestReconciler(t)
	logReader := &fakeCheckJobLogReader{
		logs: "Reading Package Lists...\nInst glibc-core [2.35-alt1] (2.35-alt2 p10:updates [x86_64])\n",
	}
	reconciler.CheckLogReader = logReader

	namespace := createEnvtestNamespace(t, ctx, c)
	policy := createEnvtestAltAptSimulationInputs(t, ctx, c, namespace)
	checkJob := createEnvtestCompletedCheckJobForPolicy(t, ctx, c, policy)
	createEnvtestCheckPodForJob(t, ctx, c, checkJob, "check-pod-updates")

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: policy.Name}})
	if err != nil {
		t.Fatalf("reconcile completed updates-available Check Job: %v", err)
	}
	if result.RequeueAfter != buildJobRequeueAfter {
		t.Fatalf("result = %#v, want build job requeue", result)
	}

	assertEnvtestCheckJobCount(t, ctx, c, namespace, 1)
	assertEnvtestBuildJobCount(t, ctx, c, namespace, 1)

	buildID := run.BuildID(policy)
	buildJobName := jobs.BuildJobName(policy.Name, buildID)
	var buildJob batchv1.Job
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: buildJobName}, &buildJob); err != nil {
		t.Fatalf("get created Build Job %q: %v", buildJobName, err)
	}
	if buildJob.Labels[jobs.LabelJobType] != string(jobs.JobTypeBuild) {
		t.Fatalf("Build Job type label = %q, want %q", buildJob.Labels[jobs.LabelJobType], jobs.JobTypeBuild)
	}

	updated := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseBuilding {
		t.Fatalf("phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseBuilding)
	}
	if updated.Status.LastCheckJobName != checkJob.Name {
		t.Fatalf("lastCheckJobName = %q, want %q", updated.Status.LastCheckJobName, checkJob.Name)
	}
	if updated.Status.LastBuildJobName != buildJobName {
		t.Fatalf("lastBuildJobName = %q, want %q", updated.Status.LastBuildJobName, buildJobName)
	}
	checkCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionCheckCompleted)
	if checkCompleted == nil || checkCompleted.Status != metav1.ConditionTrue {
		t.Fatalf("CheckCompleted condition = %#v, want True", checkCompleted)
	}
	updatesAvailable := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionUpdatesAvailable)
	if updatesAvailable == nil || updatesAvailable.Status != metav1.ConditionTrue {
		t.Fatalf("UpdatesAvailable condition = %#v, want True", updatesAvailable)
	}
}

func TestEnvtestAltAptSimulationUpdatesAvailableReusesCompletedBuildPipeline(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)
	reconciler := newEnvtestReconciler(t)
	patchClient := &patchCountingClient{Client: c}
	reconciler.Client = patchClient
	logReader := &fakeCheckJobLogReader{
		logs: "The following packages will be upgraded:\n  openssl\n1 upgraded, 0 newly installed, 0 removed and 0 not upgraded.\n",
	}
	reconciler.CheckLogReader = logReader

	namespace := createEnvtestNamespace(t, ctx, c)
	policy := envtestAltAptSimulationPolicy(namespace)
	if err := c.Create(ctx, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	policy = getEnvtestPolicy(t, ctx, c, namespace, policy.Name)

	buildID := run.BuildID(policy)
	builtImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}
	deployment := envtestDeployment(namespace, policy.Spec.TargetRef.Name, policy.Spec.ContainerName)
	deployment.Spec.Template.Spec.Containers[0].Image = builtImage
	deployment.Spec.Template.Annotations = operatordeploy.ImagePatch{
		ContainerName:   policy.Spec.ContainerName,
		Image:           builtImage,
		BuildID:         buildID,
		AppliedAt:       "2026-05-02T14:00:00Z",
		PolicyName:      policy.Name,
		PolicyNamespace: policy.Namespace,
	}.RequiredAnnotations()
	configMap := envtestBuildContextConfigMap(namespace, policy)
	for _, object := range []client.Object{deployment, configMap} {
		if err := c.Create(ctx, object); err != nil {
			t.Fatalf("create %T: %v", object, err)
		}
	}
	markEnvtestDeploymentRolledOut(t, ctx, c, namespace, deployment.Name)

	checkJob := createEnvtestCompletedCheckJobForPolicy(t, ctx, c, policy)
	createEnvtestCheckPodForJob(t, ctx, c, checkJob, "check-pod-reuse")
	buildJob := createEnvtestCompletedBuildJobForPolicy(t, ctx, c, policy)

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: policy.Name}})
	if err != nil {
		t.Fatalf("reconcile completed check/build pipeline: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want terminal success without explicit requeue", result)
	}
	if patchClient.patchCalls != 0 {
		t.Fatalf("deployment patch calls = %d, want 0 when completed build image is already applied", patchClient.patchCalls)
	}

	updated := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseSucceeded {
		t.Fatalf("phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseSucceeded)
	}
	if updated.Status.LastCheckJobName != checkJob.Name {
		t.Fatalf("lastCheckJobName = %q, want %q", updated.Status.LastCheckJobName, checkJob.Name)
	}
	if updated.Status.LastBuildJobName != buildJob.Name {
		t.Fatalf("lastBuildJobName = %q, want %q", updated.Status.LastBuildJobName, buildJob.Name)
	}
	if updated.Status.LastBuiltImage != builtImage || updated.Status.LastAppliedImage != builtImage {
		t.Fatalf("built/applied image = %q/%q, want %q", updated.Status.LastBuiltImage, updated.Status.LastAppliedImage, builtImage)
	}
	ready := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("Ready condition = %#v, want True", ready)
	}
}

func TestEnvtestAltAptSimulationUnavailableLogsSetFailedStatus(t *testing.T) {
	ctx := context.Background()
	c := envtestClientForTest(t)
	reconciler := newEnvtestReconciler(t)
	logReader := &fakeCheckJobLogReader{err: context.Canceled}
	reconciler.CheckLogReader = logReader

	namespace := createEnvtestNamespace(t, ctx, c)
	policy := createEnvtestAltAptSimulationInputs(t, ctx, c, namespace)
	checkJob := createEnvtestCompletedCheckJobForPolicy(t, ctx, c, policy)
	createEnvtestCheckPodForJob(t, ctx, c, checkJob, "check-pod-log-error")

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: policy.Name}})
	if err != nil {
		t.Fatalf("reconcile completed Check Job with unavailable logs: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want terminal failed status without explicit requeue", result)
	}
	if logReader.calls != 1 {
		t.Fatalf("log reader calls = %d, want 1", logReader.calls)
	}

	updated := getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseFailed {
		t.Fatalf("phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseFailed)
	}
	if updated.Status.Reason != reasonCheckLogsUnavailable {
		t.Fatalf("reason = %q, want %q", updated.Status.Reason, reasonCheckLogsUnavailable)
	}
	if updated.Status.LastCheckTime == nil {
		t.Fatalf("lastCheckTime is nil, want terminal check timestamp")
	}
	if updated.Status.LastBuildJobName != "" {
		t.Fatalf("lastBuildJobName = %q, want empty after log read failure", updated.Status.LastBuildJobName)
	}
	assertEnvtestCheckJobCount(t, ctx, c, namespace, 1)
	assertEnvtestBuildJobCount(t, ctx, c, namespace, 0)

	checkCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionCheckCompleted)
	if checkCompleted == nil || checkCompleted.Status != metav1.ConditionFalse {
		t.Fatalf("CheckCompleted condition = %#v, want False", checkCompleted)
	}
	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil || failed.Status != metav1.ConditionTrue {
		t.Fatalf("Failed condition = %#v, want True", failed)
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

func envtestAltAptSimulationPolicy(namespace string) *securityv1alpha1.AltImageUpdatePolicy {
	policy := envtestPolicy(namespace)
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	return policy
}

func createEnvtestAltAptSimulationInputs(t *testing.T, ctx context.Context, c client.Client, namespace string) *securityv1alpha1.AltImageUpdatePolicy {
	t.Helper()

	policy := envtestAltAptSimulationPolicy(namespace)
	deployment := envtestDeployment(namespace, policy.Spec.TargetRef.Name, policy.Spec.ContainerName)
	configMap := envtestBuildContextConfigMap(namespace, policy)
	for _, object := range []client.Object{deployment, configMap, policy} {
		if err := c.Create(ctx, object); err != nil {
			t.Fatalf("create %T: %v", object, err)
		}
	}
	return getEnvtestPolicy(t, ctx, c, namespace, policy.Name)
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

func envtestBuildContextConfigMap(namespace string, policy *securityv1alpha1.AltImageUpdatePolicy) *corev1.ConfigMap {
	configMap := testConfigMap(policy.Spec.Build.Context.ConfigMapRef.Name, map[string]string{
		policy.Spec.Build.Context.ConfigMapRef.DockerfileKey: "FROM registry.altlinux.org/alt/alt:p10\n",
	})
	configMap.Namespace = namespace
	return configMap
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

func getEnvtestJob(t *testing.T, ctx context.Context, c client.Client, namespace, name string) *batchv1.Job {
	t.Helper()

	var job batchv1.Job
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &job); err != nil {
		t.Fatalf("get job %s/%s: %v", namespace, name, err)
	}
	return &job
}

func listEnvtestJobs(t *testing.T, ctx context.Context, c client.Client, namespace string) []batchv1.Job {
	t.Helper()

	var jobList batchv1.JobList
	if err := c.List(ctx, &jobList, client.InNamespace(namespace)); err != nil {
		t.Fatalf("list jobs in namespace %q: %v", namespace, err)
	}
	return jobList.Items
}

func assertEnvtestCheckJobCount(t *testing.T, ctx context.Context, c client.Client, namespace string, want int) {
	t.Helper()

	checkJobs := 0
	for _, job := range listEnvtestJobs(t, ctx, c, namespace) {
		if job.Labels[jobs.LabelJobType] == string(jobs.JobTypeCheck) {
			checkJobs++
		}
	}
	if checkJobs != want {
		t.Fatalf("Check Job count = %d, want %d", checkJobs, want)
	}
}

func assertEnvtestBuildJobCount(t *testing.T, ctx context.Context, c client.Client, namespace string, want int) {
	t.Helper()

	buildJobs := 0
	for _, job := range listEnvtestJobs(t, ctx, c, namespace) {
		if job.Labels[jobs.LabelJobType] == string(jobs.JobTypeBuild) {
			buildJobs++
		}
	}
	if buildJobs != want {
		t.Fatalf("Build Job count = %d, want %d", buildJobs, want)
	}
}

func markEnvtestDeploymentRolledOut(t *testing.T, ctx context.Context, c client.Client, namespace, name string) *appsv1.Deployment {
	t.Helper()

	deployment := getEnvtestDeployment(t, ctx, c, namespace, name)
	replicas := int32(1)
	if deployment.Spec.Replicas != nil {
		replicas = *deployment.Spec.Replicas
	}
	deployment.Status.ObservedGeneration = deployment.Generation
	deployment.Status.Replicas = replicas
	deployment.Status.UpdatedReplicas = replicas
	deployment.Status.ReadyReplicas = replicas
	deployment.Status.AvailableReplicas = replicas
	deployment.Status.Conditions = []appsv1.DeploymentCondition{
		{
			Type:   appsv1.DeploymentProgressing,
			Status: corev1.ConditionTrue,
			Reason: "NewReplicaSetAvailable",
		},
		{
			Type:   appsv1.DeploymentAvailable,
			Status: corev1.ConditionTrue,
			Reason: "MinimumReplicasAvailable",
		},
	}
	if err := c.Status().Update(ctx, deployment); err != nil {
		t.Fatalf("status update rolled out deployment %s/%s: %v", namespace, name, err)
	}
	return getEnvtestDeployment(t, ctx, c, namespace, name)
}

func createEnvtestCompletedCheckJobForPolicy(t *testing.T, ctx context.Context, c client.Client, policy *securityv1alpha1.AltImageUpdatePolicy) *batchv1.Job {
	t.Helper()

	checkJob, err := jobs.NewCheckJob(policy, jobs.CheckJobOptions{
		RunKey:  run.RunKey(policy),
		BuildID: run.BuildID(policy),
	})
	if err != nil {
		t.Fatalf("NewCheckJob returned error: %v", err)
	}
	if err := c.Create(ctx, checkJob); err != nil {
		t.Fatalf("create Check Job: %v", err)
	}
	return markEnvtestJobComplete(t, ctx, c, checkJob.Namespace, checkJob.Name)
}

func createEnvtestCompletedBuildJobForPolicy(t *testing.T, ctx context.Context, c client.Client, policy *securityv1alpha1.AltImageUpdatePolicy) *batchv1.Job {
	t.Helper()

	buildID := run.BuildID(policy)
	builtImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}
	buildJob, err := jobs.NewBuildJob(policy, jobs.BuildJobOptions{
		RunKey:     run.RunKey(policy),
		BuildID:    buildID,
		BuiltImage: builtImage,
	})
	if err != nil {
		t.Fatalf("NewBuildJob returned error: %v", err)
	}
	if err := c.Create(ctx, buildJob); err != nil {
		t.Fatalf("create Build Job: %v", err)
	}
	return markEnvtestJobComplete(t, ctx, c, buildJob.Namespace, buildJob.Name)
}

func markEnvtestJobComplete(t *testing.T, ctx context.Context, c client.Client, namespace, name string) *batchv1.Job {
	t.Helper()

	job := getEnvtestJob(t, ctx, c, namespace, name)
	job.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	if err := c.Status().Update(ctx, job); err != nil {
		t.Fatalf("status update completed Job %s/%s: %v", namespace, name, err)
	}
	return getEnvtestJob(t, ctx, c, namespace, name)
}

func createEnvtestCheckPodForJob(t *testing.T, ctx context.Context, c client.Client, checkJob *batchv1.Job, name string) *corev1.Pod {
	t.Helper()

	controller := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: checkJob.Namespace,
			Labels: map[string]string{
				batchv1.JobNameLabel:       checkJob.Name,
				batchv1.ControllerUidLabel: string(checkJob.UID),
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: batchv1.SchemeGroupVersion.String(),
				Kind:       "Job",
				Name:       checkJob.Name,
				UID:        checkJob.UID,
				Controller: &controller,
			}},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    jobs.CheckContainerName,
				Image:   "registry.altlinux.org/alt/alt:p10",
				Command: []string{"/bin/sh", "-ec", "true"},
			}},
		},
	}
	if err := c.Create(ctx, pod); err != nil {
		t.Fatalf("create Check Job pod %s/%s: %v", pod.Namespace, pod.Name, err)
	}
	return pod
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
