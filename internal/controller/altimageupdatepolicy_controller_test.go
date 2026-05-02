package controller

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
	operatorcheck "alt-image-update-operator/internal/check"
	operatordeploy "alt-image-update-operator/internal/deploy"
	operatorimage "alt-image-update-operator/internal/image"
	"alt-image-update-operator/internal/jobs"
	"alt-image-update-operator/internal/run"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNamespace = "default"

func TestReconcileAltAptSimulationCreatesOneCheckJobAndCheckingStatus(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
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

	jobList := listJobs(t, ctx, reconciler.Client)
	if len(jobList.Items) != 1 {
		t.Fatalf("Jobs len = %d, want exactly one Check Job", len(jobList.Items))
	}

	checkJob := jobList.Items[0]
	buildID := run.BuildID(policy)
	wantJobName := jobs.CheckJobName(policy.Name, buildID)
	if checkJob.Name != wantJobName {
		t.Fatalf("check Job name = %q, want %q", checkJob.Name, wantJobName)
	}
	if checkJob.Labels[jobs.LabelJobType] != string(jobs.JobTypeCheck) {
		t.Fatalf("job type label = %q, want %q", checkJob.Labels[jobs.LabelJobType], jobs.JobTypeCheck)
	}
	if checkJob.Annotations[jobs.AnnotationRunKey] != run.RunKey(policy) {
		t.Fatalf("run key annotation = %q, want %q", checkJob.Annotations[jobs.AnnotationRunKey], run.RunKey(policy))
	}
	if len(checkJob.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences len = %d, want 1", len(checkJob.OwnerReferences))
	}
	if len(checkJob.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("check Job containers len = %d, want 1", len(checkJob.Spec.Template.Spec.Containers))
	}
	container := checkJob.Spec.Template.Spec.Containers[0]
	if container.Name != jobs.CheckContainerName {
		t.Fatalf("check container name = %q, want %q", container.Name, jobs.CheckContainerName)
	}
	if container.Image != policy.Spec.Alt.BaseImage {
		t.Fatalf("check container image = %q, want %q", container.Image, policy.Spec.Alt.BaseImage)
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
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseChecking {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseChecking)
	}
	if updated.Status.Reason != reasonCheckJobRunning {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, reasonCheckJobRunning)
	}
	if updated.Status.LastCheckJobName != wantJobName {
		t.Fatalf("LastCheckJobName = %q, want %q", updated.Status.LastCheckJobName, wantJobName)
	}
	if updated.Status.LastCheckTime != nil {
		t.Fatalf("LastCheckTime = %v, want nil while check is running", updated.Status.LastCheckTime)
	}
	if updated.Status.LastBuildJobName != "" {
		t.Fatalf("LastBuildJobName = %q, want empty before check result is parsed", updated.Status.LastBuildJobName)
	}
	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil {
		t.Fatalf("missing %s condition", securityv1alpha1.ConditionFailed)
	}
	if failed.Status != metav1.ConditionFalse {
		t.Fatalf("Failed condition status = %q, want %q", failed.Status, metav1.ConditionFalse)
	}
	checkCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionCheckCompleted)
	if checkCompleted == nil || checkCompleted.Status != metav1.ConditionUnknown {
		t.Fatalf("CheckCompleted condition = %#v, want Unknown while check is running", checkCompleted)
	}
}

func TestReconcileRunningCheckJobKeepsCheckingAndRequeues(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	checkJob := testCheckJobForPolicy(t, policy)
	checkJob.Status.Active = 1
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		checkJob,
	)

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.RequeueAfter != checkJobRequeueAfter {
		t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, checkJobRequeueAfter)
	}

	jobList := listJobs(t, ctx, reconciler.Client)
	if len(jobList.Items) != 1 {
		t.Fatalf("Jobs len = %d, want existing Check Job only", len(jobList.Items))
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseChecking {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseChecking)
	}
	if updated.Status.Reason != reasonCheckJobRunning {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, reasonCheckJobRunning)
	}
	if updated.Status.LastCheckJobName != checkJob.Name {
		t.Fatalf("LastCheckJobName = %q, want %q", updated.Status.LastCheckJobName, checkJob.Name)
	}
	if updated.Status.LastBuildJobName != "" {
		t.Fatalf("LastBuildJobName = %q, want empty while check is running", updated.Status.LastBuildJobName)
	}
}

func TestReconcileFailedCheckJobSetsFailedStatus(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	checkJob := testCheckJobForPolicy(t, policy)
	checkJob.Status.Conditions = []batchv1.JobCondition{
		{
			Type:    batchv1.JobFailed,
			Status:  corev1.ConditionTrue,
			Reason:  "BackoffLimitExceeded",
			Message: "Job has reached the specified backoff limit",
		},
	}
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		checkJob,
	)

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want no explicit requeue after failed check", result)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseFailed {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseFailed)
	}
	if updated.Status.Reason != reasonCheckJobFailed {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, reasonCheckJobFailed)
	}
	if updated.Status.LastCheckJobName != checkJob.Name {
		t.Fatalf("LastCheckJobName = %q, want %q", updated.Status.LastCheckJobName, checkJob.Name)
	}
	if updated.Status.LastCheckTime == nil {
		t.Fatalf("LastCheckTime is nil, want terminal check timestamp")
	}
	if updated.Status.LastBuildJobName != "" {
		t.Fatalf("LastBuildJobName = %q, want empty after failed check", updated.Status.LastBuildJobName)
	}
	if updated.Status.Message == "" || !containsString([]string{updated.Status.Message}, `Check Job "`+checkJob.Name+`" failed: BackoffLimitExceeded: Job has reached the specified backoff limit`) {
		t.Fatalf("Message = %q, want useful failed Check Job message", updated.Status.Message)
	}

	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil || failed.Status != metav1.ConditionTrue {
		t.Fatalf("Failed condition = %#v, want True", failed)
	}
	checkCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionCheckCompleted)
	if checkCompleted == nil || checkCompleted.Status != metav1.ConditionFalse {
		t.Fatalf("CheckCompleted condition = %#v, want False", checkCompleted)
	}
}

func TestReconcileCompletedCheckJobNoUpdatesSetsUpToDate(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	checkJob := testCheckJobForPolicy(t, policy)
	checkJob.UID = "check-job-uid"
	checkJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	ownedPod := testCheckPodForJob(checkJob, "check-pod-owned", corev1.PodSucceeded)
	ownedPod.CreationTimestamp = metav1.NewTime(time.Date(2026, 5, 2, 13, 18, 0, 0, time.UTC))
	unrelatedPod := testDeploymentPod("check-pod-unrelated")
	unrelatedPod.Labels = map[string]string{batchv1.JobNameLabel: checkJob.Name}
	logReader := &fakeCheckJobLogReader{
		logs: "Reading Package Lists...\nsuper-secret-token\n0 upgraded, 0 newly installed, 0 removed and 0 not upgraded.\n",
	}
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		checkJob,
		ownedPod,
		unrelatedPod,
	)
	reconciler.CheckLogReader = logReader

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want no explicit requeue after completed check", result)
	}
	if logReader.calls != 1 {
		t.Fatalf("log reader calls = %d, want 1", logReader.calls)
	}
	if logReader.lastNamespace != policy.Namespace {
		t.Fatalf("log reader namespace = %q, want %q", logReader.lastNamespace, policy.Namespace)
	}
	if logReader.lastPodName != ownedPod.Name {
		t.Fatalf("log reader pod = %q, want owned pod %q", logReader.lastPodName, ownedPod.Name)
	}
	if logReader.lastContainerName != jobs.CheckContainerName {
		t.Fatalf("log reader container = %q, want %q", logReader.lastContainerName, jobs.CheckContainerName)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseUpToDate {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseUpToDate)
	}
	if updated.Status.Reason != reasonNoUpdates {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, reasonNoUpdates)
	}
	if updated.Status.LastCheckJobName != checkJob.Name {
		t.Fatalf("LastCheckJobName = %q, want %q", updated.Status.LastCheckJobName, checkJob.Name)
	}
	if updated.Status.LastCheckTime == nil {
		t.Fatalf("LastCheckTime is nil, want completed check timestamp")
	}
	if updated.Status.LastBuildJobName != "" {
		t.Fatalf("LastBuildJobName = %q, want empty when no updates are available", updated.Status.LastBuildJobName)
	}
	if statusContains(updated.Status, "super-secret-token") {
		t.Fatalf("status contains raw check logs: %#v", updated.Status)
	}
	checkCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionCheckCompleted)
	if checkCompleted == nil || checkCompleted.Status != metav1.ConditionTrue {
		t.Fatalf("CheckCompleted condition = %#v, want True", checkCompleted)
	}
	ready := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("Ready condition = %#v, want True", ready)
	}
	updatesAvailable := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionUpdatesAvailable)
	if updatesAvailable == nil || updatesAvailable.Status != metav1.ConditionFalse {
		t.Fatalf("UpdatesAvailable condition = %#v, want False", updatesAvailable)
	}
	upToDate := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionUpToDate)
	if upToDate == nil || upToDate.Status != metav1.ConditionTrue {
		t.Fatalf("UpToDate condition = %#v, want True", upToDate)
	}
	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil || failed.Status != metav1.ConditionFalse {
		t.Fatalf("Failed condition = %#v, want False", failed)
	}

	jobList := listJobs(t, ctx, reconciler.Client)
	if len(jobList.Items) != 1 || jobList.Items[0].Name != checkJob.Name {
		t.Fatalf("Jobs = %#v, want only Check Job %q", jobList.Items, checkJob.Name)
	}
}

func TestReconcileCompletedCheckJobUpdatesAvailableCreatesBuildJob(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	checkJob := testCheckJobForPolicy(t, policy)
	checkJob.UID = "check-job-uid"
	checkJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	ownedPod := testCheckPodForJob(checkJob, "check-pod-owned", corev1.PodSucceeded)
	logReader := &fakeCheckJobLogReader{
		logs: "Reading Package Lists...\nInst glibc-core [2.35-alt1] (2.35-alt2 p10:updates [x86_64])\n",
	}
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		checkJob,
		ownedPod,
	)
	reconciler.CheckLogReader = logReader

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.RequeueAfter != buildJobRequeueAfter {
		t.Fatalf("RequeueAfter = %s, want %s for newly created Build Job", result.RequeueAfter, buildJobRequeueAfter)
	}

	jobList := listJobs(t, ctx, reconciler.Client)
	if len(jobList.Items) != 2 {
		t.Fatalf("Jobs len = %d, want Check Job and Build Job", len(jobList.Items))
	}
	buildID := run.BuildID(policy)
	buildJobName := jobs.BuildJobName(policy.Name, buildID)
	buildJob := findJob(jobList.Items, buildJobName)
	if buildJob == nil {
		t.Fatalf("Build Job %q was not created; jobs = %#v", buildJobName, jobList.Items)
	}
	if buildJob.Labels[jobs.LabelJobType] != string(jobs.JobTypeBuild) {
		t.Fatalf("Build Job type label = %q, want %q", buildJob.Labels[jobs.LabelJobType], jobs.JobTypeBuild)
	}
	wantImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}
	if !containsString(buildJob.Spec.Template.Spec.Containers[0].Args, "--destination="+wantImage) {
		t.Fatalf("Build Job args = %v, want destination %q", buildJob.Spec.Template.Spec.Containers[0].Args, wantImage)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseBuilding {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseBuilding)
	}
	if updated.Status.LastCheckJobName != checkJob.Name {
		t.Fatalf("LastCheckJobName = %q, want %q", updated.Status.LastCheckJobName, checkJob.Name)
	}
	if updated.Status.LastCheckTime == nil {
		t.Fatalf("LastCheckTime is nil, want completed check timestamp")
	}
	if updated.Status.LastBuildJobName != buildJobName {
		t.Fatalf("LastBuildJobName = %q, want %q", updated.Status.LastBuildJobName, buildJobName)
	}
	if updated.Status.LastBuildStartTime == nil {
		t.Fatalf("LastBuildStartTime is nil, want build start timestamp")
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

func TestReconcileCompletedCheckJobUpdatesAvailableReusesBuildPipeline(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	checkJob := testCheckJobForPolicy(t, policy)
	checkJob.UID = "check-job-uid"
	checkJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	buildJob := testBuildJobForPolicy(t, policy)
	buildJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	buildID := run.BuildID(policy)
	wantImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}
	deployment := rolledOutDeploymentForPolicy(policy, wantImage, buildID, 8)
	ownedPod := testCheckPodForJob(checkJob, "check-pod-owned", corev1.PodSucceeded)
	logReader := &fakeCheckJobLogReader{
		logs: "The following packages will be upgraded:\n  openssl\n1 upgraded, 0 newly installed, 0 removed and 0 not upgraded.\n",
	}
	reconciler := newTestReconciler(t,
		policy,
		deployment,
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		checkJob,
		buildJob,
		ownedPod,
	)
	reconciler.CheckLogReader = logReader

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want no explicit requeue after successful reused pipeline", result)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseSucceeded {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseSucceeded)
	}
	if updated.Status.LastCheckJobName != checkJob.Name {
		t.Fatalf("LastCheckJobName = %q, want %q", updated.Status.LastCheckJobName, checkJob.Name)
	}
	if updated.Status.LastBuildJobName != buildJob.Name {
		t.Fatalf("LastBuildJobName = %q, want %q", updated.Status.LastBuildJobName, buildJob.Name)
	}
	if updated.Status.LastBuiltImage != wantImage || updated.Status.LastAppliedImage != wantImage {
		t.Fatalf("built/applied image = %q/%q, want %q", updated.Status.LastBuiltImage, updated.Status.LastAppliedImage, wantImage)
	}
	checkCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionCheckCompleted)
	if checkCompleted == nil || checkCompleted.Status != metav1.ConditionTrue {
		t.Fatalf("CheckCompleted condition = %#v, want True", checkCompleted)
	}
	updatesAvailable := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionUpdatesAvailable)
	if updatesAvailable == nil || updatesAvailable.Status != metav1.ConditionTrue {
		t.Fatalf("UpdatesAvailable condition = %#v, want True", updatesAvailable)
	}
	ready := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("Ready condition = %#v, want True", ready)
	}
}

func TestReconcileEmitsPipelineMilestoneEventsOnce(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	checkJob := testCheckJobForPolicy(t, policy)
	checkJob.UID = "check-job-uid"
	checkJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	buildJob := testBuildJobForPolicy(t, policy)
	buildJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	buildID := run.BuildID(policy)
	wantImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}
	deployment := rolledOutDeploymentForPolicy(policy, wantImage, buildID, 8)
	ownedPod := testCheckPodForJob(checkJob, "check-pod-owned", corev1.PodSucceeded)
	logReader := &fakeCheckJobLogReader{
		logs: "The following packages will be upgraded:\n  openssl\n1 upgraded, 0 newly installed, 0 removed and 0 not upgraded.\n",
	}
	reconciler := newTestReconciler(t,
		policy,
		deployment,
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		checkJob,
		buildJob,
		ownedPod,
	)
	reconciler.CheckLogReader = logReader

	if _, err := reconciler.Reconcile(ctx, requestFor(policy)); err != nil {
		t.Fatalf("first Reconcile returned error: %v", err)
	}

	events := recordedEvents(t, reconciler)
	assertEventReasons(t, events,
		eventReasonCheckStarted,
		eventReasonCheckCompleted,
		eventReasonBuildStarted,
		eventReasonBuildCompleted,
		eventReasonDeploymentUpdated,
		eventReasonRolloutCompleted,
		eventReasonPolicySucceeded,
	)
	assertEventsContain(t, events, policy.Namespace+"/"+policy.Name)
	assertEventsContain(t, events, checkJob.Name)
	assertEventsContain(t, events, buildJob.Name)
	assertEventsContain(t, events, wantImage)

	if _, err := reconciler.Reconcile(ctx, requestFor(policy)); err != nil {
		t.Fatalf("second Reconcile returned error: %v", err)
	}
	if events := recordedEvents(t, reconciler); len(events) != 0 {
		t.Fatalf("events after terminal current-run reconcile = %v, want none", events)
	}
}

func TestReconcileCompletedCheckJobParserFailureSetsFailedStatus(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	checkJob := testCheckJobForPolicy(t, policy)
	checkJob.UID = "check-job-uid"
	checkJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	ownedPod := testCheckPodForJob(checkJob, "check-pod-owned", corev1.PodSucceeded)
	logReader := &fakeCheckJobLogReader{
		logs: "Reading Package Lists...\nE: Failed to fetch package index\n",
	}
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		checkJob,
		ownedPod,
	)
	reconciler.CheckLogReader = logReader

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want no explicit requeue after failed check classification", result)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseFailed {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseFailed)
	}
	if updated.Status.Reason != operatorcheck.ReasonAptCommandFailed {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, operatorcheck.ReasonAptCommandFailed)
	}
	if updated.Status.LastCheckTime == nil {
		t.Fatalf("LastCheckTime is nil, want completed check timestamp")
	}
	if updated.Status.LastBuildJobName != "" {
		t.Fatalf("LastBuildJobName = %q, want empty after failed check classification", updated.Status.LastBuildJobName)
	}
	if statusContains(updated.Status, "E: Failed") {
		t.Fatalf("status contains raw check log evidence: %#v", updated.Status)
	}
	checkCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionCheckCompleted)
	if checkCompleted == nil || checkCompleted.Status != metav1.ConditionFalse {
		t.Fatalf("CheckCompleted condition = %#v, want False", checkCompleted)
	}
	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil || failed.Status != metav1.ConditionTrue {
		t.Fatalf("Failed condition = %#v, want True", failed)
	}
}

func TestReconcileUpToDateCurrentRunDoesNotCreateBuildJobOrMutateStatus(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	checkJob := testCheckJobForPolicy(t, policy)
	checkJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	markPolicyUpToDateForCurrentRun(policy, checkJob.Name)
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		checkJob,
	)
	beforeStatus := *getPolicy(t, ctx, reconciler.Client, policy.Name).Status.DeepCopy()

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want no explicit requeue for current UpToDate run", result)
	}

	jobList := listJobs(t, ctx, reconciler.Client)
	if len(jobList.Items) != 1 || jobList.Items[0].Name != checkJob.Name {
		t.Fatalf("Jobs = %#v, want only existing Check Job %q", jobList.Items, checkJob.Name)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if !reflect.DeepEqual(beforeStatus, updated.Status) {
		t.Fatalf("status changed for current UpToDate run:\n before: %#v\n  after: %#v", beforeStatus, updated.Status)
	}
}

func TestReconcileCompletedCheckJobWithoutOwnedPodSetsLogsUnavailable(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	checkJob := testCheckJobForPolicy(t, policy)
	checkJob.UID = "check-job-uid"
	checkJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	logReader := &fakeCheckJobLogReader{logs: "should not be read"}
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		checkJob,
		testDeploymentPod("unowned-pod"),
	)
	reconciler.CheckLogReader = logReader

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want no explicit requeue after terminal log failure", result)
	}
	if logReader.calls != 0 {
		t.Fatalf("log reader calls = %d, want 0 when owned pod is missing", logReader.calls)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseFailed {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseFailed)
	}
	if updated.Status.Reason != reasonCheckLogsUnavailable {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, reasonCheckLogsUnavailable)
	}
	if !strings.Contains(updated.Status.Message, "no pod owned by Check Job") {
		t.Fatalf("Message = %q, want missing owned pod explanation", updated.Status.Message)
	}
	if updated.Status.LastCheckTime == nil {
		t.Fatalf("LastCheckTime is nil, want terminal check timestamp")
	}
	checkCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionCheckCompleted)
	if checkCompleted == nil || checkCompleted.Status != metav1.ConditionFalse {
		t.Fatalf("CheckCompleted condition = %#v, want False", checkCompleted)
	}
}

func TestReconcileCompletedCheckJobLogReadFailureSetsLogsUnavailable(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	policy.Spec.Check.Mode = securityv1alpha1.CheckModeAltAptSimulation
	checkJob := testCheckJobForPolicy(t, policy)
	checkJob.UID = "check-job-uid"
	checkJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	ownedPod := testCheckPodForJob(checkJob, "check-pod-owned", corev1.PodSucceeded)
	logReader := &fakeCheckJobLogReader{err: errors.New("kubelet log stream unavailable")}
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		checkJob,
		ownedPod,
	)
	reconciler.CheckLogReader = logReader

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want no explicit requeue after terminal log failure", result)
	}
	if logReader.calls != 1 {
		t.Fatalf("log reader calls = %d, want 1", logReader.calls)
	}
	if logReader.lastPodName != ownedPod.Name {
		t.Fatalf("log reader pod = %q, want %q", logReader.lastPodName, ownedPod.Name)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseFailed {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseFailed)
	}
	if updated.Status.Reason != reasonCheckLogsUnavailable {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, reasonCheckLogsUnavailable)
	}
	if !strings.Contains(updated.Status.Message, "kubelet log stream unavailable") {
		t.Fatalf("Message = %q, want log read error", updated.Status.Message)
	}
	checkCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionCheckCompleted)
	if checkCompleted == nil || checkCompleted.Status != metav1.ConditionFalse {
		t.Fatalf("CheckCompleted condition = %#v, want False", checkCompleted)
	}
	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil || failed.Status != metav1.ConditionTrue {
		t.Fatalf("Failed condition = %#v, want True", failed)
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

func TestReconcileRunningBuildJobKeepsBuildingAndRequeues(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	buildJob := testBuildJobForPolicy(t, policy)
	buildJob.Status.Active = 1
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		buildJob,
	)

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.RequeueAfter != 5*time.Second {
		t.Fatalf("RequeueAfter = %s, want 5s", result.RequeueAfter)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseBuilding {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseBuilding)
	}
	if updated.Status.Reason != reasonBuildJobRunning {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, reasonBuildJobRunning)
	}
	if updated.Status.LastBuildJobName != buildJob.Name {
		t.Fatalf("LastBuildJobName = %q, want %q", updated.Status.LastBuildJobName, buildJob.Name)
	}
	if updated.Status.LastBuiltImage != "" {
		t.Fatalf("LastBuiltImage = %q, want empty while build is running", updated.Status.LastBuiltImage)
	}
}

func TestReconcileCompletedBuildJobPatchesDeploymentAndRecordsAppliedImage(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	buildJob := testBuildJobForPolicy(t, policy)
	buildJob.Status.Conditions = []batchv1.JobCondition{
		{
			Type:    batchv1.JobComplete,
			Status:  corev1.ConditionTrue,
			Reason:  "CompletionsReached",
			Message: "Job completed",
		},
	}
	deployment := testDeployment("demo-app", "app", "sidecar")
	deployment.Spec.Template.Annotations = map[string]string{"example.com/user": "kept"}
	deployment.Spec.Template.Spec.Containers[1].Image = "registry.example.test/sidecar:v1"
	reconciler := newTestReconciler(t,
		policy,
		deployment,
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		buildJob,
	)
	countingClient := &patchCountingClient{Client: reconciler.Client}
	reconciler.Client = countingClient

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.RequeueAfter != rolloutRequeueAfter {
		t.Fatalf("RequeueAfter = %s, want %s while rollout is progressing", result.RequeueAfter, rolloutRequeueAfter)
	}

	buildID := run.BuildID(policy)
	wantImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}
	if countingClient.patchCalls != 1 {
		t.Fatalf("Patch calls = %d, want 1 Deployment patch", countingClient.patchCalls)
	}

	updatedDeployment := getDeployment(t, ctx, reconciler.Client, "demo-app")
	if updatedDeployment.Spec.Template.Spec.Containers[0].Image != wantImage {
		t.Fatalf("app image = %q, want %q", updatedDeployment.Spec.Template.Spec.Containers[0].Image, wantImage)
	}
	if updatedDeployment.Spec.Template.Spec.Containers[1].Image != "registry.example.test/sidecar:v1" {
		t.Fatalf("sidecar image = %q, want unchanged sidecar image", updatedDeployment.Spec.Template.Spec.Containers[1].Image)
	}
	annotations := updatedDeployment.Spec.Template.Annotations
	if annotations["example.com/user"] != "kept" {
		t.Fatalf("user annotation = %q, want kept", annotations["example.com/user"])
	}
	if annotations[operatordeploy.AnnotationLastBuildID] != buildID {
		t.Fatalf("last build id annotation = %q, want %q", annotations[operatordeploy.AnnotationLastBuildID], buildID)
	}
	if annotations[operatordeploy.AnnotationLastBuiltImage] != wantImage {
		t.Fatalf("last built image annotation = %q, want %q", annotations[operatordeploy.AnnotationLastBuiltImage], wantImage)
	}
	if annotations[operatordeploy.AnnotationPolicyName] != policy.Name || annotations[operatordeploy.AnnotationPolicyNamespace] != policy.Namespace {
		t.Fatalf("policy annotations = %#v, want policy identity", annotations)
	}
	if annotations[operatordeploy.AnnotationLastAppliedAt] == "" {
		t.Fatalf("last applied annotation is empty, want timestamp")
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseRollingOut {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseRollingOut)
	}
	if updated.Status.Reason == reasonDeploymentPatched {
		t.Fatalf("Reason = %q, want rollout observation reason after Deployment patch", updated.Status.Reason)
	}
	if updated.Status.LastBuildJobName != buildJob.Name {
		t.Fatalf("LastBuildJobName = %q, want %q", updated.Status.LastBuildJobName, buildJob.Name)
	}
	if updated.Status.LastBuiltImage != wantImage {
		t.Fatalf("LastBuiltImage = %q, want %q", updated.Status.LastBuiltImage, wantImage)
	}
	if updated.Status.LastBuildCompletionTime == nil {
		t.Fatalf("LastBuildCompletionTime is nil, want completion timestamp")
	}
	if updated.Status.LastAppliedImage != wantImage {
		t.Fatalf("LastAppliedImage = %q, want %q", updated.Status.LastAppliedImage, wantImage)
	}
	if updated.Status.LastApplyTime == nil {
		t.Fatalf("LastApplyTime is nil, want apply timestamp")
	}
	if updated.Status.TargetDeploymentGeneration != updatedDeployment.Generation {
		t.Fatalf("TargetDeploymentGeneration = %d, want %d", updated.Status.TargetDeploymentGeneration, updatedDeployment.Generation)
	}

	buildCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionBuildCompleted)
	if buildCompleted == nil || buildCompleted.Status != metav1.ConditionTrue {
		t.Fatalf("BuildCompleted condition = %#v, want True", buildCompleted)
	}
	imagePublished := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionImagePublished)
	if imagePublished == nil || imagePublished.Status != metav1.ConditionTrue {
		t.Fatalf("ImagePublished condition = %#v, want True", imagePublished)
	}
	deploymentUpdated := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionDeploymentUpdated)
	if deploymentUpdated == nil || deploymentUpdated.Status != metav1.ConditionTrue {
		t.Fatalf("DeploymentUpdated condition = %#v, want True", deploymentUpdated)
	}
	rolloutCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionRolloutCompleted)
	if rolloutCompleted == nil || rolloutCompleted.Status != metav1.ConditionUnknown {
		t.Fatalf("RolloutCompleted condition = %#v, want Unknown while rollout is progressing", rolloutCompleted)
	}
}

func TestReconcileCompletedBuildJobSkipsDeploymentPatchWhenAlreadyApplied(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	buildJob := testBuildJobForPolicy(t, policy)
	buildJob.Status.Conditions = []batchv1.JobCondition{
		{
			Type:   batchv1.JobComplete,
			Status: corev1.ConditionTrue,
		},
	}
	buildID := run.BuildID(policy)
	wantImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}

	deployment := testDeployment("demo-app", "app")
	deployment.Spec.Template.Spec.Containers[0].Image = wantImage
	deployment.Spec.Template.Annotations = operatordeploy.ImagePatch{
		ContainerName:   policy.Spec.ContainerName,
		Image:           wantImage,
		BuildID:         buildID,
		AppliedAt:       "2026-05-02T13:00:00Z",
		PolicyName:      policy.Name,
		PolicyNamespace: policy.Namespace,
	}.RequiredAnnotations()
	reconciler := newTestReconciler(t,
		policy,
		deployment,
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		buildJob,
	)
	countingClient := &patchCountingClient{Client: reconciler.Client}
	reconciler.Client = countingClient

	if _, err := reconciler.Reconcile(ctx, requestFor(policy)); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if countingClient.patchCalls != 0 {
		t.Fatalf("Patch calls = %d, want no Deployment patch", countingClient.patchCalls)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseRollingOut {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseRollingOut)
	}
	if updated.Status.Reason == reasonDeploymentAlreadyUpdated {
		t.Fatalf("Reason = %q, want rollout observation reason after already-applied Deployment", updated.Status.Reason)
	}
	if updated.Status.LastAppliedImage != wantImage {
		t.Fatalf("LastAppliedImage = %q, want %q", updated.Status.LastAppliedImage, wantImage)
	}
	if updated.Status.LastApplyTime == nil || updated.Status.LastApplyTime.Time.UTC().Format(time.RFC3339) != "2026-05-02T13:00:00Z" {
		t.Fatalf("LastApplyTime = %v, want existing apply annotation timestamp", updated.Status.LastApplyTime)
	}
}

func TestReconcileSuccessfulRolloutSetsSucceededReadyAndRolloutTime(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	buildJob := testBuildJobForPolicy(t, policy)
	buildJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	buildID := run.BuildID(policy)
	wantImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}

	replicas := int32(2)
	deployment := testDeployment("demo-app", "app")
	deployment.Generation = 8
	deployment.Spec.Replicas = &replicas
	deployment.Spec.Template.Spec.Containers[0].Image = wantImage
	deployment.Spec.Template.Annotations = operatordeploy.ImagePatch{
		ContainerName:   policy.Spec.ContainerName,
		Image:           wantImage,
		BuildID:         buildID,
		AppliedAt:       "2026-05-02T13:00:00Z",
		PolicyName:      policy.Name,
		PolicyNamespace: policy.Namespace,
	}.RequiredAnnotations()
	deployment.Status.ObservedGeneration = 8
	deployment.Status.Replicas = replicas
	deployment.Status.UpdatedReplicas = replicas
	deployment.Status.AvailableReplicas = replicas

	reconciler := newTestReconciler(t,
		policy,
		deployment,
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		buildJob,
	)

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want no explicit requeue after successful rollout", result)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseSucceeded {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseSucceeded)
	}
	if updated.Status.Reason != reasonRolloutCompleted {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, reasonRolloutCompleted)
	}
	if updated.Status.LastBuiltImage != wantImage {
		t.Fatalf("LastBuiltImage = %q, want %q", updated.Status.LastBuiltImage, wantImage)
	}
	if updated.Status.LastAppliedImage != wantImage {
		t.Fatalf("LastAppliedImage = %q, want %q", updated.Status.LastAppliedImage, wantImage)
	}
	if updated.Status.LastRolloutTime == nil {
		t.Fatalf("LastRolloutTime is nil, want rollout completion timestamp")
	}

	ready := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("Ready condition = %#v, want True", ready)
	}
	rolloutCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionRolloutCompleted)
	if rolloutCompleted == nil || rolloutCompleted.Status != metav1.ConditionTrue {
		t.Fatalf("RolloutCompleted condition = %#v, want True", rolloutCompleted)
	}
	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil || failed.Status != metav1.ConditionFalse {
		t.Fatalf("Failed condition = %#v, want False", failed)
	}
}

func TestReconcileSucceededCurrentRunDoesNotCreatePatchOrMutateStatus(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	buildID := run.BuildID(policy)
	builtImage := markPolicySucceededForCurrentRun(t, policy, 8)
	buildJob := testBuildJobForPolicy(t, policy)
	buildJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	deployment := rolledOutDeploymentForPolicy(policy, builtImage, buildID, 8)

	reconciler := newTestReconciler(t,
		policy,
		deployment,
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		buildJob,
	)
	countingClient := &patchCountingClient{Client: reconciler.Client}
	reconciler.Client = countingClient
	beforeStatus := *getPolicy(t, ctx, reconciler.Client, policy.Name).Status.DeepCopy()

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want no explicit requeue for terminal current run", result)
	}
	if countingClient.patchCalls != 0 {
		t.Fatalf("Patch calls = %d, want no Deployment patch for terminal current run", countingClient.patchCalls)
	}

	buildJobs := listJobs(t, ctx, reconciler.Client)
	if len(buildJobs.Items) != 1 {
		t.Fatalf("build Jobs len = %d, want existing Job only", len(buildJobs.Items))
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if !reflect.DeepEqual(beforeStatus, updated.Status) {
		t.Fatalf("status changed for terminal current run:\n before: %#v\n  after: %#v", beforeStatus, updated.Status)
	}
}

func TestReconcileManualTokenChangeStartsNewRunBuildJobAndImageTag(t *testing.T) {
	ctx := context.Background()
	previousPolicy := testPolicy()
	previousPolicy.Spec.Trigger.ManualToken = "first-run"
	oldBuildID := run.BuildID(previousPolicy)
	oldBuiltImage := markPolicySucceededForCurrentRun(t, previousPolicy, 8)
	oldBuildJob := testBuildJobForPolicy(t, previousPolicy)
	oldBuildJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}

	policy := previousPolicy.DeepCopy()
	policy.Generation++
	policy.Spec.Trigger.ManualToken = "second-run"
	newBuildID := run.BuildID(policy)
	newBuiltImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, newBuildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}
	if newBuildID == oldBuildID {
		t.Fatalf("new BuildID = %q, want different from previous %q", newBuildID, oldBuildID)
	}
	if newBuiltImage == oldBuiltImage {
		t.Fatalf("new built image = %q, want different from previous %q", newBuiltImage, oldBuiltImage)
	}

	reconciler := newTestReconciler(t,
		policy,
		rolledOutDeploymentForPolicy(previousPolicy, oldBuiltImage, oldBuildID, 8),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		oldBuildJob,
	)

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.RequeueAfter != buildJobRequeueAfter {
		t.Fatalf("RequeueAfter = %s, want %s for new running build", result.RequeueAfter, buildJobRequeueAfter)
	}

	buildJobs := listJobs(t, ctx, reconciler.Client)
	if len(buildJobs.Items) != 2 {
		t.Fatalf("build Jobs len = %d, want previous and new Jobs", len(buildJobs.Items))
	}
	newJobName := jobs.BuildJobName(policy.Name, newBuildID)
	newJob := findJob(buildJobs.Items, newJobName)
	if newJob == nil {
		t.Fatalf("new build Job %q was not created; jobs = %#v", newJobName, buildJobs.Items)
	}
	if newJob.Annotations[jobs.AnnotationRunKey] != run.RunKey(policy) {
		t.Fatalf("new Job run key annotation = %q, want %q", newJob.Annotations[jobs.AnnotationRunKey], run.RunKey(policy))
	}
	if !containsString(newJob.Spec.Template.Spec.Containers[0].Args, "--destination="+newBuiltImage) {
		t.Fatalf("new build Job args = %v, want destination %q", newJob.Spec.Template.Spec.Containers[0].Args, newBuiltImage)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.CurrentRunKey != run.RunKey(policy) {
		t.Fatalf("CurrentRunKey = %q, want %q", updated.Status.CurrentRunKey, run.RunKey(policy))
	}
	if updated.Status.BuildID != newBuildID {
		t.Fatalf("BuildID = %q, want %q", updated.Status.BuildID, newBuildID)
	}
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseBuilding {
		t.Fatalf("Phase = %q, want %q for new run", updated.Status.Phase, securityv1alpha1.PolicyPhaseBuilding)
	}
	if updated.Status.LastBuildJobName != newJobName {
		t.Fatalf("LastBuildJobName = %q, want %q", updated.Status.LastBuildJobName, newJobName)
	}
	if updated.Status.LastBuildStartTime == nil {
		t.Fatalf("LastBuildStartTime is nil, want timestamp for new run")
	}
	if updated.Status.LastBuiltImage != "" || updated.Status.LastAppliedImage != "" || updated.Status.LastRolloutTime != nil {
		t.Fatalf("new run kept previous output status: lastBuilt=%q lastApplied=%q lastRollout=%v", updated.Status.LastBuiltImage, updated.Status.LastAppliedImage, updated.Status.LastRolloutTime)
	}
}

func TestReconcileFailedRolloutSetsFailedStatus(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	buildJob := testBuildJobForPolicy(t, policy)
	buildJob.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	buildID := run.BuildID(policy)
	wantImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}

	deployment := testDeployment("demo-app", "app")
	deployment.Generation = 6
	deployment.Spec.Template.Spec.Containers[0].Image = wantImage
	deployment.Spec.Template.Annotations = operatordeploy.ImagePatch{
		ContainerName:   policy.Spec.ContainerName,
		Image:           wantImage,
		BuildID:         buildID,
		AppliedAt:       "2026-05-02T13:00:00Z",
		PolicyName:      policy.Name,
		PolicyNamespace: policy.Namespace,
	}.RequiredAnnotations()
	deployment.Status.ObservedGeneration = 6
	deployment.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:    appsv1.DeploymentProgressing,
		Status:  corev1.ConditionFalse,
		Reason:  "ProgressDeadlineExceeded",
		Message: "ReplicaSet demo-app-abc has timed out progressing.",
	}}

	reconciler := newTestReconciler(t,
		policy,
		deployment,
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		buildJob,
	)

	result, err := reconciler.Reconcile(ctx, requestFor(policy))
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("result = %#v, want no explicit requeue after failed rollout", result)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseFailed {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseFailed)
	}
	if updated.Status.Reason != reasonRolloutFailed {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, reasonRolloutFailed)
	}
	if updated.Status.LastBuiltImage != wantImage {
		t.Fatalf("LastBuiltImage = %q, want %q", updated.Status.LastBuiltImage, wantImage)
	}
	if updated.Status.LastAppliedImage != wantImage {
		t.Fatalf("LastAppliedImage = %q, want %q", updated.Status.LastAppliedImage, wantImage)
	}

	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil || failed.Status != metav1.ConditionTrue {
		t.Fatalf("Failed condition = %#v, want True", failed)
	}
	rolloutCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionRolloutCompleted)
	if rolloutCompleted == nil || rolloutCompleted.Status != metav1.ConditionFalse {
		t.Fatalf("RolloutCompleted condition = %#v, want False", rolloutCompleted)
	}
}

func TestReconcileFailedBuildJobSetsFailedStatus(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	buildJob := testBuildJobForPolicy(t, policy)
	buildJob.Status.Conditions = []batchv1.JobCondition{
		{
			Type:    batchv1.JobFailed,
			Status:  corev1.ConditionTrue,
			Reason:  "BackoffLimitExceeded",
			Message: "Job has reached the specified backoff limit",
		},
	}
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		buildJob,
	)

	if _, err := reconciler.Reconcile(ctx, requestFor(policy)); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	updated := getPolicy(t, ctx, reconciler.Client, policy.Name)
	if updated.Status.Phase != securityv1alpha1.PolicyPhaseFailed {
		t.Fatalf("Phase = %q, want %q", updated.Status.Phase, securityv1alpha1.PolicyPhaseFailed)
	}
	if updated.Status.Reason != reasonBuildJobFailed {
		t.Fatalf("Reason = %q, want %q", updated.Status.Reason, reasonBuildJobFailed)
	}
	if updated.Status.LastBuildJobName != buildJob.Name {
		t.Fatalf("LastBuildJobName = %q, want %q", updated.Status.LastBuildJobName, buildJob.Name)
	}
	if updated.Status.LastBuiltImage != "" {
		t.Fatalf("LastBuiltImage = %q, want empty after failed build", updated.Status.LastBuiltImage)
	}
	if updated.Status.Message == "" || !containsString([]string{updated.Status.Message}, `Build Job "`+buildJob.Name+`" failed: BackoffLimitExceeded: Job has reached the specified backoff limit`) {
		t.Fatalf("Message = %q, want useful failed Job message", updated.Status.Message)
	}

	failed := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionFailed)
	if failed == nil || failed.Status != metav1.ConditionTrue {
		t.Fatalf("Failed condition = %#v, want True", failed)
	}
	buildCompleted := findCondition(updated.Status.Conditions, securityv1alpha1.ConditionBuildCompleted)
	if buildCompleted == nil || buildCompleted.Status != metav1.ConditionFalse {
		t.Fatalf("BuildCompleted condition = %#v, want False", buildCompleted)
	}
}

func TestReconcileEmitsFailureEvents(t *testing.T) {
	ctx := context.Background()
	policy := testPolicy()
	buildJob := testBuildJobForPolicy(t, policy)
	buildJob.Status.Conditions = []batchv1.JobCondition{
		{
			Type:    batchv1.JobFailed,
			Status:  corev1.ConditionTrue,
			Reason:  "BackoffLimitExceeded",
			Message: "Job has reached the specified backoff limit",
		},
	}
	reconciler := newTestReconciler(t,
		policy,
		testDeployment("demo-app", "app"),
		testConfigMap("demo-context", map[string]string{"Dockerfile": "FROM registry.altlinux.org/alt/alt:p10\n"}),
		buildJob,
	)

	if _, err := reconciler.Reconcile(ctx, requestFor(policy)); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	events := recordedEvents(t, reconciler)
	assertEventReasons(t, events,
		eventReasonBuildStarted,
		eventReasonBuildFailed,
		eventReasonPolicyFailed,
	)
	assertEventsContain(t, events, policy.Namespace+"/"+policy.Name)
	assertEventsContain(t, events, buildJob.Name)
	if eventsContain(events, "pull-secret") {
		t.Fatalf("events unexpectedly contain Secret data/name: %v", events)
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
		Scheme:         scheme,
		CheckLogReader: &fakeCheckJobLogReader{},
		Recorder:       record.NewFakeRecorder(100),
	}
}

type fakeCheckJobLogReader struct {
	logs              string
	err               error
	calls             int
	lastNamespace     string
	lastPodName       string
	lastContainerName string
}

func (r *fakeCheckJobLogReader) ReadLogs(_ context.Context, namespace, podName, containerName string) (string, error) {
	r.calls++
	r.lastNamespace = namespace
	r.lastPodName = podName
	r.lastContainerName = containerName
	if r.err != nil {
		return "", r.err
	}
	return r.logs, nil
}

type patchCountingClient struct {
	client.Client
	patchCalls int
}

func (c *patchCountingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.patchCalls++
	return c.Client.Patch(ctx, obj, patch, opts...)
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

func testBuildJobForPolicy(t *testing.T, policy *securityv1alpha1.AltImageUpdatePolicy) *batchv1.Job {
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
	return buildJob
}

func testCheckJobForPolicy(t *testing.T, policy *securityv1alpha1.AltImageUpdatePolicy) *batchv1.Job {
	t.Helper()

	checkJob, err := jobs.NewCheckJob(policy, jobs.CheckJobOptions{
		RunKey:  run.RunKey(policy),
		BuildID: run.BuildID(policy),
	})
	if err != nil {
		t.Fatalf("NewCheckJob returned error: %v", err)
	}
	return checkJob
}

func testCheckPodForJob(job *batchv1.Job, name string, phase corev1.PodPhase) *corev1.Pod {
	controller := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: job.Namespace,
			Labels: map[string]string{
				batchv1.JobNameLabel:       job.Name,
				batchv1.ControllerUidLabel: string(job.UID),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: batchv1.SchemeGroupVersion.String(),
					Kind:       "Job",
					Name:       job.Name,
					UID:        job.UID,
					Controller: &controller,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: phase,
		},
	}
}

func testDeploymentPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
	}
}

func markPolicySucceededForCurrentRun(t *testing.T, policy *securityv1alpha1.AltImageUpdatePolicy, targetDeploymentGeneration int64) string {
	t.Helper()

	buildID := run.BuildID(policy)
	builtImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}
	statusTime := metav1.NewTime(time.Date(2026, 5, 2, 13, 0, 0, 0, time.UTC))
	message := "Deployment rollout completed for image " + builtImage
	policy.Status = securityv1alpha1.AltImageUpdatePolicyStatus{
		ObservedGeneration:         policy.Generation,
		Phase:                      securityv1alpha1.PolicyPhaseSucceeded,
		BuildID:                    buildID,
		CurrentRunKey:              run.RunKey(policy),
		LastBuildStartTime:         &statusTime,
		LastBuildCompletionTime:    &statusTime,
		LastApplyTime:              &statusTime,
		LastRolloutTime:            &statusTime,
		LastBuildJobName:           jobs.BuildJobName(policy.Name, buildID),
		LastBuiltImage:             builtImage,
		LastAppliedImage:           builtImage,
		TargetDeploymentGeneration: targetDeploymentGeneration,
		Reason:                     reasonRolloutCompleted,
		Message:                    message,
		Conditions: []metav1.Condition{
			terminalCondition(policy.Generation, securityv1alpha1.ConditionReady, metav1.ConditionTrue, reasonRolloutCompleted, message, statusTime),
			terminalCondition(policy.Generation, securityv1alpha1.ConditionFailed, metav1.ConditionFalse, reasonRolloutCompleted, message, statusTime),
			terminalCondition(policy.Generation, securityv1alpha1.ConditionBuildCompleted, metav1.ConditionTrue, reasonBuildJobCompleted, message, statusTime),
			terminalCondition(policy.Generation, securityv1alpha1.ConditionImagePublished, metav1.ConditionTrue, reasonBuildJobCompleted, message, statusTime),
			terminalCondition(policy.Generation, securityv1alpha1.ConditionDeploymentUpdated, metav1.ConditionTrue, reasonDeploymentAlreadyUpdated, message, statusTime),
			terminalCondition(policy.Generation, securityv1alpha1.ConditionRolloutCompleted, metav1.ConditionTrue, reasonRolloutCompleted, message, statusTime),
		},
	}
	return builtImage
}

func markPolicyUpToDateForCurrentRun(policy *securityv1alpha1.AltImageUpdatePolicy, checkJobName string) {
	statusTime := metav1.NewTime(time.Date(2026, 5, 2, 13, 20, 0, 0, time.UTC))
	message := "Check Job found no ALT package updates"
	policy.Status = securityv1alpha1.AltImageUpdatePolicyStatus{
		ObservedGeneration: policy.Generation,
		Phase:              securityv1alpha1.PolicyPhaseUpToDate,
		BuildID:            run.BuildID(policy),
		CurrentRunKey:      run.RunKey(policy),
		LastCheckTime:      &statusTime,
		LastCheckJobName:   checkJobName,
		Reason:             reasonNoUpdates,
		Message:            message,
		Conditions: []metav1.Condition{
			terminalCondition(policy.Generation, securityv1alpha1.ConditionReady, metav1.ConditionTrue, reasonNoUpdates, message, statusTime),
			terminalCondition(policy.Generation, securityv1alpha1.ConditionCheckCompleted, metav1.ConditionTrue, reasonNoUpdates, message, statusTime),
			terminalCondition(policy.Generation, securityv1alpha1.ConditionUpdatesAvailable, metav1.ConditionFalse, reasonNoUpdates, message, statusTime),
			terminalCondition(policy.Generation, securityv1alpha1.ConditionUpToDate, metav1.ConditionTrue, reasonNoUpdates, message, statusTime),
			terminalCondition(policy.Generation, securityv1alpha1.ConditionFailed, metav1.ConditionFalse, reasonNoUpdates, message, statusTime),
		},
	}
}

func terminalCondition(generation int64, conditionType string, status metav1.ConditionStatus, reason, message string, transitionTime metav1.Time) metav1.Condition {
	return metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: generation,
		LastTransitionTime: transitionTime,
		Reason:             reason,
		Message:            message,
	}
}

func rolledOutDeploymentForPolicy(policy *securityv1alpha1.AltImageUpdatePolicy, builtImage, buildID string, generation int64) *appsv1.Deployment {
	replicas := int32(1)
	deployment := testDeployment(policy.Spec.TargetRef.Name, policy.Spec.ContainerName)
	deployment.Generation = generation
	deployment.Spec.Replicas = &replicas
	deployment.Spec.Template.Spec.Containers[0].Image = builtImage
	deployment.Spec.Template.Annotations = operatordeploy.ImagePatch{
		ContainerName:   policy.Spec.ContainerName,
		Image:           builtImage,
		BuildID:         buildID,
		AppliedAt:       "2026-05-02T13:00:00Z",
		PolicyName:      policy.Name,
		PolicyNamespace: policy.Namespace,
	}.RequiredAnnotations()
	deployment.Status.ObservedGeneration = generation
	deployment.Status.Replicas = replicas
	deployment.Status.UpdatedReplicas = replicas
	deployment.Status.AvailableReplicas = replicas
	return deployment
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

func getDeployment(t *testing.T, ctx context.Context, c client.Client, name string) *appsv1.Deployment {
	t.Helper()

	var deployment appsv1.Deployment
	if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, &deployment); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	return &deployment
}

func listJobs(t *testing.T, ctx context.Context, c client.Client) *batchv1.JobList {
	t.Helper()

	var jobList batchv1.JobList
	if err := c.List(ctx, &jobList, client.InNamespace(testNamespace)); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	return &jobList
}

func findJob(jobs []batchv1.Job, name string) *batchv1.Job {
	for i := range jobs {
		if jobs[i].Name == name {
			return &jobs[i]
		}
	}
	return nil
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

func statusContains(status securityv1alpha1.AltImageUpdatePolicyStatus, value string) bool {
	if strings.Contains(status.Reason, value) || strings.Contains(status.Message, value) {
		return true
	}
	for i := range status.Conditions {
		if strings.Contains(status.Conditions[i].Reason, value) || strings.Contains(status.Conditions[i].Message, value) {
			return true
		}
	}
	return false
}

func recordedEvents(t *testing.T, reconciler *AltImageUpdatePolicyReconciler) []string {
	t.Helper()

	recorder, ok := reconciler.Recorder.(*record.FakeRecorder)
	if !ok {
		t.Fatalf("Recorder = %T, want *record.FakeRecorder", reconciler.Recorder)
	}

	var events []string
	for {
		select {
		case event := <-recorder.Events:
			events = append(events, event)
		default:
			return events
		}
	}
}

func assertEventReasons(t *testing.T, events []string, wantReasons ...string) {
	t.Helper()

	if len(events) != len(wantReasons) {
		t.Fatalf("events len = %d, want %d\n events: %v", len(events), len(wantReasons), events)
	}
	for _, reason := range wantReasons {
		needle := " " + reason + " "
		if !eventsContain(events, needle) {
			t.Fatalf("missing event reason %q in events: %v", reason, events)
		}
		if count := countEventsContaining(events, needle); count != 1 {
			t.Fatalf("event reason %q count = %d, want 1 in events: %v", reason, count, events)
		}
	}
}

func assertEventsContain(t *testing.T, events []string, value string) {
	t.Helper()

	if !eventsContain(events, value) {
		t.Fatalf("events do not contain %q: %v", value, events)
	}
}

func eventsContain(events []string, value string) bool {
	return countEventsContaining(events, value) > 0
}

func countEventsContaining(events []string, value string) int {
	count := 0
	for _, event := range events {
		if strings.Contains(event, value) {
			count++
		}
	}
	return count
}
