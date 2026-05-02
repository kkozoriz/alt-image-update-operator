package deploy

import (
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	RolloutStateProgressing = "Progressing"
	RolloutStateSucceeded   = "Succeeded"
	RolloutStateFailed      = "Failed"

	ReasonDeploymentNotObserved    = "DeploymentNotObserved"
	ReasonProgressDeadlineExceeded = "ProgressDeadlineExceeded"
	ReasonReplicaFailure           = "ReplicaFailure"
	ReasonRolloutComplete          = "RolloutComplete"
	ReasonRolloutTimedOut          = "RolloutTimedOut"
	ReasonRolloutReplicasNotReady  = "RolloutReplicasNotReady"
	defaultDeploymentReplicaCount  = int32(1)
	progressDeadlineExceededReason = "ProgressDeadlineExceeded"
)

// RolloutOptions describes the controller-owned rollout inputs.
type RolloutOptions struct {
	// TargetGeneration is the Deployment generation recorded immediately after the image patch.
	// If it is zero, the helper uses deployment.Generation.
	TargetGeneration int64
	// StartedAt is when the controller started observing the rollout.
	StartedAt time.Time
	// Now is the current controller time. If zero, timeout evaluation is skipped.
	Now time.Time
	// Timeout is the maximum time the controller is willing to wait for convergence.
	Timeout time.Duration
}

// RolloutObservation is the pure rollout classification returned to the reconciler.
type RolloutObservation struct {
	State              string
	Reason             string
	Message            string
	DesiredReplicas    int32
	UpdatedReplicas    int32
	AvailableReplicas  int32
	ObservedGeneration int64
	RequiredGeneration int64
}

// ObserveRollout classifies Deployment rollout state without sleeping or using the API server.
func ObserveRollout(deployment *appsv1.Deployment, options RolloutOptions) (RolloutObservation, error) {
	if deployment == nil {
		return RolloutObservation{}, errors.New("deployment is nil")
	}

	desired := desiredReplicas(deployment)
	requiredGeneration := requiredRolloutGeneration(deployment, options.TargetGeneration)
	observation := RolloutObservation{
		DesiredReplicas:    desired,
		UpdatedReplicas:    deployment.Status.UpdatedReplicas,
		AvailableReplicas:  deployment.Status.AvailableReplicas,
		ObservedGeneration: deployment.Status.ObservedGeneration,
		RequiredGeneration: requiredGeneration,
	}

	if deployment.Status.ObservedGeneration < requiredGeneration {
		if rolloutTimedOut(options) {
			return observation.withState(
				RolloutStateFailed,
				ReasonRolloutTimedOut,
				fmt.Sprintf("deployment %s/%s did not observe generation %d before rollout timeout", deployment.Namespace, deployment.Name, requiredGeneration),
			), nil
		}
		return observation.withState(
			RolloutStateProgressing,
			ReasonDeploymentNotObserved,
			fmt.Sprintf("deployment %s/%s observed generation %d, waiting for generation %d", deployment.Namespace, deployment.Name, deployment.Status.ObservedGeneration, requiredGeneration),
		), nil
	}

	if condition := findDeploymentCondition(deployment, appsv1.DeploymentProgressing); condition != nil &&
		condition.Status == corev1.ConditionFalse &&
		condition.Reason == progressDeadlineExceededReason {
		return observation.withState(
			RolloutStateFailed,
			ReasonProgressDeadlineExceeded,
			conditionMessage(condition, "deployment progress deadline exceeded"),
		), nil
	}

	if condition := findDeploymentCondition(deployment, appsv1.DeploymentReplicaFailure); condition != nil &&
		condition.Status == corev1.ConditionTrue {
		return observation.withState(
			RolloutStateFailed,
			ReasonReplicaFailure,
			conditionMessage(condition, "deployment reported replica failure"),
		), nil
	}

	if deploymentComplete(deployment, desired) {
		return observation.withState(
			RolloutStateSucceeded,
			ReasonRolloutComplete,
			fmt.Sprintf("deployment %s/%s rollout completed with %d desired replicas", deployment.Namespace, deployment.Name, desired),
		), nil
	}

	if rolloutTimedOut(options) {
		return observation.withState(
			RolloutStateFailed,
			ReasonRolloutTimedOut,
			fmt.Sprintf("deployment %s/%s rollout did not complete before timeout", deployment.Namespace, deployment.Name),
		), nil
	}

	return observation.withState(
		RolloutStateProgressing,
		ReasonRolloutReplicasNotReady,
		fmt.Sprintf("deployment %s/%s has %d/%d updated replicas and %d/%d available replicas", deployment.Namespace, deployment.Name, deployment.Status.UpdatedReplicas, desired, deployment.Status.AvailableReplicas, desired),
	), nil
}

func (o RolloutObservation) withState(state, reason, message string) RolloutObservation {
	o.State = state
	o.Reason = reason
	o.Message = message
	return o
}

func requiredRolloutGeneration(deployment *appsv1.Deployment, targetGeneration int64) int64 {
	if targetGeneration > deployment.Generation {
		return targetGeneration
	}
	return deployment.Generation
}

func desiredReplicas(deployment *appsv1.Deployment) int32 {
	if deployment.Spec.Replicas == nil {
		return defaultDeploymentReplicaCount
	}
	return *deployment.Spec.Replicas
}

func deploymentComplete(deployment *appsv1.Deployment, desired int32) bool {
	return deployment.Status.UpdatedReplicas == desired &&
		deployment.Status.Replicas == desired &&
		deployment.Status.AvailableReplicas == desired &&
		deployment.Status.UnavailableReplicas == 0
}

func rolloutTimedOut(options RolloutOptions) bool {
	if options.Timeout <= 0 || options.StartedAt.IsZero() || options.Now.IsZero() {
		return false
	}
	return !options.Now.Before(options.StartedAt.Add(options.Timeout))
}

func findDeploymentCondition(deployment *appsv1.Deployment, conditionType appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range deployment.Status.Conditions {
		if deployment.Status.Conditions[i].Type == conditionType {
			return &deployment.Status.Conditions[i]
		}
	}
	return nil
}

func conditionMessage(condition *appsv1.DeploymentCondition, fallback string) string {
	if condition.Message != "" {
		return condition.Message
	}
	return fallback
}
