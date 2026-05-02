package status

import (
	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConditionReady             = securityv1alpha1.ConditionReady
	ConditionCheckCompleted    = securityv1alpha1.ConditionCheckCompleted
	ConditionUpdatesAvailable  = securityv1alpha1.ConditionUpdatesAvailable
	ConditionBuildCompleted    = securityv1alpha1.ConditionBuildCompleted
	ConditionImagePublished    = securityv1alpha1.ConditionImagePublished
	ConditionDeploymentUpdated = securityv1alpha1.ConditionDeploymentUpdated
	ConditionRolloutCompleted  = securityv1alpha1.ConditionRolloutCompleted
	ConditionUpToDate          = securityv1alpha1.ConditionUpToDate
	ConditionFailed            = securityv1alpha1.ConditionFailed
)

// SetCondition creates or updates a Kubernetes-style condition.
//
// The caller supplies now so reconcile tests can use deterministic timestamps.
// lastTransitionTime changes when the condition state tuple
// (status, reason, message) changes; observedGeneration-only updates preserve it.
func SetCondition(conditions *[]metav1.Condition, generation int64, conditionType string, conditionStatus metav1.ConditionStatus, reason, message string, now metav1.Time) bool {
	if conditions == nil {
		return false
	}

	next := metav1.Condition{
		Type:               conditionType,
		Status:             conditionStatus,
		ObservedGeneration: generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	}

	for i := range *conditions {
		current := &(*conditions)[i]
		if current.Type != conditionType {
			continue
		}

		stateChanged := current.Status != conditionStatus || current.Reason != reason || current.Message != message
		observedGenerationChanged := current.ObservedGeneration != generation
		if !stateChanged && !observedGenerationChanged {
			return false
		}

		if !stateChanged {
			next.LastTransitionTime = current.LastTransitionTime
		}
		(*conditions)[i] = next
		return true
	}

	*conditions = append(*conditions, next)
	return true
}

// MarkReady records successful completion of the policy workflow.
func MarkReady(policyStatus *securityv1alpha1.AltImageUpdatePolicyStatus, generation int64, now metav1.Time, reason, message string) bool {
	return applyConditionSet(policyStatus, generation, PhaseSucceeded, reason, message, now, []conditionUpdate{
		{conditionType: ConditionReady, status: metav1.ConditionTrue, reason: reason, message: message},
		{conditionType: ConditionFailed, status: metav1.ConditionFalse, reason: reason, message: message},
	})
}

// MarkChecking records that update detection is in progress.
func MarkChecking(policyStatus *securityv1alpha1.AltImageUpdatePolicyStatus, generation int64, now metav1.Time, reason, message string) bool {
	return applyConditionSet(policyStatus, generation, PhaseChecking, reason, message, now, []conditionUpdate{
		{conditionType: ConditionReady, status: metav1.ConditionFalse, reason: reason, message: message},
		{conditionType: ConditionCheckCompleted, status: metav1.ConditionUnknown, reason: reason, message: message},
		{conditionType: ConditionFailed, status: metav1.ConditionFalse, reason: reason, message: message},
	})
}

// MarkBuilding records that image build and push is in progress.
func MarkBuilding(policyStatus *securityv1alpha1.AltImageUpdatePolicyStatus, generation int64, now metav1.Time, reason, message string) bool {
	return applyConditionSet(policyStatus, generation, PhaseBuilding, reason, message, now, []conditionUpdate{
		{conditionType: ConditionReady, status: metav1.ConditionFalse, reason: reason, message: message},
		{conditionType: ConditionBuildCompleted, status: metav1.ConditionUnknown, reason: reason, message: message},
		{conditionType: ConditionImagePublished, status: metav1.ConditionUnknown, reason: reason, message: message},
		{conditionType: ConditionFailed, status: metav1.ConditionFalse, reason: reason, message: message},
	})
}

// MarkApplying records that the built image is being applied to the target Deployment.
func MarkApplying(policyStatus *securityv1alpha1.AltImageUpdatePolicyStatus, generation int64, now metav1.Time, reason, message string) bool {
	return applyConditionSet(policyStatus, generation, PhaseApplying, reason, message, now, []conditionUpdate{
		{conditionType: ConditionReady, status: metav1.ConditionFalse, reason: reason, message: message},
		{conditionType: ConditionDeploymentUpdated, status: metav1.ConditionUnknown, reason: reason, message: message},
		{conditionType: ConditionFailed, status: metav1.ConditionFalse, reason: reason, message: message},
	})
}

// MarkRollout records that the target Deployment rollout is being observed.
func MarkRollout(policyStatus *securityv1alpha1.AltImageUpdatePolicyStatus, generation int64, now metav1.Time, reason, message string) bool {
	return applyConditionSet(policyStatus, generation, PhaseRollingOut, reason, message, now, []conditionUpdate{
		{conditionType: ConditionReady, status: metav1.ConditionFalse, reason: reason, message: message},
		{conditionType: ConditionRolloutCompleted, status: metav1.ConditionUnknown, reason: reason, message: message},
		{conditionType: ConditionFailed, status: metav1.ConditionFalse, reason: reason, message: message},
	})
}

// MarkUpToDate records a successful no-op result when no package updates are available.
func MarkUpToDate(policyStatus *securityv1alpha1.AltImageUpdatePolicyStatus, generation int64, now metav1.Time, reason, message string) bool {
	return applyConditionSet(policyStatus, generation, PhaseUpToDate, reason, message, now, []conditionUpdate{
		{conditionType: ConditionReady, status: metav1.ConditionTrue, reason: reason, message: message},
		{conditionType: ConditionUpdatesAvailable, status: metav1.ConditionFalse, reason: reason, message: message},
		{conditionType: ConditionUpToDate, status: metav1.ConditionTrue, reason: reason, message: message},
		{conditionType: ConditionFailed, status: metav1.ConditionFalse, reason: reason, message: message},
	})
}

// MarkFailed records a terminal policy failure for the current run.
func MarkFailed(policyStatus *securityv1alpha1.AltImageUpdatePolicyStatus, generation int64, now metav1.Time, reason, message string) bool {
	return applyConditionSet(policyStatus, generation, PhaseFailed, reason, message, now, []conditionUpdate{
		{conditionType: ConditionReady, status: metav1.ConditionFalse, reason: reason, message: message},
		{conditionType: ConditionFailed, status: metav1.ConditionTrue, reason: reason, message: message},
	})
}

type conditionUpdate struct {
	conditionType string
	status        metav1.ConditionStatus
	reason        string
	message       string
}

func applyConditionSet(policyStatus *securityv1alpha1.AltImageUpdatePolicyStatus, generation int64, phase Phase, reason, message string, now metav1.Time, updates []conditionUpdate) bool {
	if policyStatus == nil {
		return false
	}

	changed := SetPhase(policyStatus, generation, phase, reason, message)
	for _, update := range updates {
		if SetCondition(&policyStatus.Conditions, generation, update.conditionType, update.status, update.reason, update.message, now) {
			changed = true
		}
	}
	return changed
}
