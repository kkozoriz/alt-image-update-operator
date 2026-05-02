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

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
	"alt-image-update-operator/internal/run"
	operatorstatus "alt-image-update-operator/internal/status"
	appsv1 "k8s.io/api/apps/v1"
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
