/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
	operatorimage "alt-image-update-operator/internal/image"
	"alt-image-update-operator/internal/jobs"
	"alt-image-update-operator/internal/run"
	operatorstatus "alt-image-update-operator/internal/status"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	reasonPreflightSucceeded       = "PreflightSucceeded"
	reasonTargetDeploymentNotFound = "TargetDeploymentNotFound"
	reasonTargetContainerNotFound  = "TargetContainerNotFound"
	reasonBuildContextNotFound     = "BuildContextNotFound"
	reasonDockerfileKeyNotFound    = "DockerfileKeyNotFound"
	reasonBuildImageInvalid        = "BuildImageInvalid"
	reasonBuildJobEnsured          = "BuildJobEnsured"
	reasonBuildJobRunning          = "BuildJobRunning"
	reasonBuildJobCompleted        = "BuildJobCompleted"
	reasonBuildJobFailed           = "BuildJobFailed"

	buildJobRequeueAfter = 5 * time.Second
)

// AltImageUpdatePolicyReconciler reconciles an AltImageUpdatePolicy object.
type AltImageUpdatePolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=security.altlinux.org,resources=altimageupdatepolicies,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=security.altlinux.org,resources=altimageupdatepolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=security.altlinux.org,resources=altimageupdatepolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=create;get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;patch;update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// Reconcile performs the policy preflight checks needed before starting check/build Jobs.
func (r *AltImageUpdatePolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var policy securityv1alpha1.AltImageUpdatePolicy
	if err := r.Get(ctx, req.NamespacedName, &policy); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	originalStatus := policy.Status.DeepCopy()
	runKey := run.RunKey(&policy)
	buildID := run.BuildID(&policy)
	now := metav1.Now()

	policy.Status.CurrentRunKey = runKey
	policy.Status.BuildID = buildID

	var deployment appsv1.Deployment
	deploymentKey := types.NamespacedName{Namespace: policy.Namespace, Name: policy.Spec.TargetRef.Name}
	if err := r.Get(ctx, deploymentKey, &deployment); err != nil {
		if errors.IsNotFound(err) {
			message := fmt.Sprintf("Target Deployment %q was not found in namespace %q", policy.Spec.TargetRef.Name, policy.Namespace)
			operatorstatus.MarkFailed(&policy.Status, policy.Generation, now, reasonTargetDeploymentNotFound, message)
			return ctrl.Result{}, r.updatePolicyStatus(ctx, &policy, originalStatus)
		}
		return ctrl.Result{}, err
	}

	if !deploymentHasContainer(&deployment, policy.Spec.ContainerName) {
		message := fmt.Sprintf("Target Deployment %q does not contain container %q", policy.Spec.TargetRef.Name, policy.Spec.ContainerName)
		operatorstatus.MarkFailed(&policy.Status, policy.Generation, now, reasonTargetContainerNotFound, message)
		return ctrl.Result{}, r.updatePolicyStatus(ctx, &policy, originalStatus)
	}

	contextRef := policy.Spec.Build.Context.ConfigMapRef
	var contextConfigMap corev1.ConfigMap
	contextKey := types.NamespacedName{Namespace: policy.Namespace, Name: contextRef.Name}
	if err := r.Get(ctx, contextKey, &contextConfigMap); err != nil {
		if errors.IsNotFound(err) {
			message := fmt.Sprintf("Build context ConfigMap %q was not found in namespace %q", contextRef.Name, policy.Namespace)
			operatorstatus.MarkFailed(&policy.Status, policy.Generation, now, reasonBuildContextNotFound, message)
			return ctrl.Result{}, r.updatePolicyStatus(ctx, &policy, originalStatus)
		}
		return ctrl.Result{}, err
	}

	if _, ok := contextConfigMap.Data[contextRef.DockerfileKey]; !ok {
		message := fmt.Sprintf("Build context ConfigMap %q does not contain Dockerfile key %q", contextRef.Name, contextRef.DockerfileKey)
		operatorstatus.MarkFailed(&policy.Status, policy.Generation, now, reasonDockerfileKeyNotFound, message)
		return ctrl.Result{}, r.updatePolicyStatus(ctx, &policy, originalStatus)
	}

	if policy.Spec.Check.Mode == securityv1alpha1.CheckModeAlways {
		builtImage, err := operatorimage.BuildReference(policy.Spec.Build.OutputImage, buildID)
		if err != nil {
			message := fmt.Sprintf("Build output image %q cannot be converted to a build tag: %v", policy.Spec.Build.OutputImage, err)
			operatorstatus.MarkFailed(&policy.Status, policy.Generation, now, reasonBuildImageInvalid, message)
			return ctrl.Result{}, r.updatePolicyStatus(ctx, &policy, originalStatus)
		}

		buildJob, err := r.ensureBuildJob(ctx, &policy, jobs.BuildJobOptions{
			RunKey:     runKey,
			BuildID:    buildID,
			BuiltImage: builtImage,
		})
		if err != nil {
			return ctrl.Result{}, err
		}

		policy.Status.LastBuildJobName = buildJob.Name
		if policy.Status.LastBuildStartTime == nil || originalStatus.LastBuildJobName != buildJob.Name {
			policy.Status.LastBuildStartTime = &now
		}

		if complete := findJobCondition(buildJob, batchv1.JobComplete); complete != nil && complete.Status == corev1.ConditionTrue {
			message := fmt.Sprintf("Build Job %q completed and published image %q", buildJob.Name, builtImage)
			operatorstatus.MarkApplying(&policy.Status, policy.Generation, now, reasonBuildJobCompleted, message)
			operatorstatus.SetCondition(&policy.Status.Conditions, policy.Generation, operatorstatus.ConditionBuildCompleted, metav1.ConditionTrue, reasonBuildJobCompleted, message, now)
			operatorstatus.SetCondition(&policy.Status.Conditions, policy.Generation, operatorstatus.ConditionImagePublished, metav1.ConditionTrue, reasonBuildJobCompleted, message, now)
			policy.Status.LastBuiltImage = builtImage
			if policy.Status.LastBuildCompletionTime == nil || originalStatus.LastBuiltImage != builtImage || originalStatus.LastBuildJobName != buildJob.Name {
				policy.Status.LastBuildCompletionTime = &now
			}
			if err := r.updatePolicyStatus(ctx, &policy, originalStatus); err != nil {
				return ctrl.Result{}, err
			}

			logger.V(1).Info("AltImageUpdatePolicy build Job completed", "generation", policy.Generation, "runKey", runKey, "buildID", buildID, "job", buildJob.Name)
			return ctrl.Result{}, nil
		}

		if failed := findJobCondition(buildJob, batchv1.JobFailed); failed != nil && failed.Status == corev1.ConditionTrue {
			message := jobFailureMessage(buildJob, failed)
			operatorstatus.MarkFailed(&policy.Status, policy.Generation, now, reasonBuildJobFailed, message)
			operatorstatus.SetCondition(&policy.Status.Conditions, policy.Generation, operatorstatus.ConditionBuildCompleted, metav1.ConditionFalse, reasonBuildJobFailed, message, now)
			operatorstatus.SetCondition(&policy.Status.Conditions, policy.Generation, operatorstatus.ConditionImagePublished, metav1.ConditionFalse, reasonBuildJobFailed, message, now)
			if err := r.updatePolicyStatus(ctx, &policy, originalStatus); err != nil {
				return ctrl.Result{}, err
			}

			logger.V(1).Info("AltImageUpdatePolicy build Job failed", "generation", policy.Generation, "runKey", runKey, "buildID", buildID, "job", buildJob.Name)
			return ctrl.Result{}, nil
		}

		message := fmt.Sprintf("Build Job %q is present for image %q", buildJob.Name, builtImage)
		if buildJob.Status.Active > 0 {
			message = fmt.Sprintf("Build Job %q is running for image %q", buildJob.Name, builtImage)
		}
		operatorstatus.MarkBuilding(&policy.Status, policy.Generation, now, reasonBuildJobRunning, message)
		if err := r.updatePolicyStatus(ctx, &policy, originalStatus); err != nil {
			return ctrl.Result{}, err
		}

		logger.V(1).Info("AltImageUpdatePolicy build Job ensured", "generation", policy.Generation, "runKey", runKey, "buildID", buildID, "job", buildJob.Name)
		return ctrl.Result{RequeueAfter: buildJobRequeueAfter}, nil
	}

	successMessage := "Policy preflight checks completed"
	operatorstatus.SetPhase(&policy.Status, policy.Generation, operatorstatus.PhasePending, reasonPreflightSucceeded, successMessage)
	operatorstatus.SetCondition(&policy.Status.Conditions, policy.Generation, operatorstatus.ConditionFailed, metav1.ConditionFalse, reasonPreflightSucceeded, successMessage, now)
	if err := r.updatePolicyStatus(ctx, &policy, originalStatus); err != nil {
		return ctrl.Result{}, err
	}

	logger.V(1).Info("AltImageUpdatePolicy preflight completed", "generation", policy.Generation, "runKey", runKey, "buildID", buildID)
	return ctrl.Result{}, nil
}

func deploymentHasContainer(deployment *appsv1.Deployment, containerName string) bool {
	if deployment == nil {
		return false
	}
	for i := range deployment.Spec.Template.Spec.Containers {
		if deployment.Spec.Template.Spec.Containers[i].Name == containerName {
			return true
		}
	}
	return false
}

func (r *AltImageUpdatePolicyReconciler) ensureBuildJob(ctx context.Context, policy *securityv1alpha1.AltImageUpdatePolicy, opts jobs.BuildJobOptions) (*batchv1.Job, error) {
	jobName := jobs.BuildJobName(policy.Name, opts.BuildID)
	jobKey := types.NamespacedName{Namespace: policy.Namespace, Name: jobName}

	var existing batchv1.Job
	if err := r.Get(ctx, jobKey, &existing); err != nil {
		if !errors.IsNotFound(err) {
			return nil, err
		}

		buildJob, err := jobs.NewBuildJob(policy, opts)
		if err != nil {
			return nil, err
		}
		if err := r.Create(ctx, buildJob); err != nil {
			if !errors.IsAlreadyExists(err) {
				return nil, err
			}
			if err := r.Get(ctx, jobKey, &existing); err != nil {
				return nil, err
			}
			return &existing, nil
		}
		return buildJob, nil
	}

	return &existing, nil
}

func findJobCondition(job *batchv1.Job, conditionType batchv1.JobConditionType) *batchv1.JobCondition {
	if job == nil {
		return nil
	}
	for i := range job.Status.Conditions {
		if job.Status.Conditions[i].Type == conditionType {
			return &job.Status.Conditions[i]
		}
	}
	return nil
}

func jobFailureMessage(job *batchv1.Job, condition *batchv1.JobCondition) string {
	if job == nil {
		return "Build Job failed"
	}

	message := fmt.Sprintf("Build Job %q failed", job.Name)
	if condition == nil {
		return message
	}
	if condition.Reason != "" {
		message += ": " + condition.Reason
	}
	if condition.Message != "" {
		message += ": " + condition.Message
	}
	return message
}

func (r *AltImageUpdatePolicyReconciler) updatePolicyStatus(ctx context.Context, policy *securityv1alpha1.AltImageUpdatePolicy, original *securityv1alpha1.AltImageUpdatePolicyStatus) error {
	if original != nil && apiequality.Semantic.DeepEqual(original, &policy.Status) {
		return nil
	}
	return r.Status().Update(ctx, policy)
}

// SetupWithManager sets up the controller with the Manager.
func (r *AltImageUpdatePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&securityv1alpha1.AltImageUpdatePolicy{}).
		Named("altimageupdatepolicy").
		Complete(r)
}
