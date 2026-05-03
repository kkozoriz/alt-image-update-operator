package status

import securityv1alpha1 "alt-image-update-operator/api/v1alpha1"

// Phase aliases the API phase type so controller code can use one status helper package.
type Phase = securityv1alpha1.PolicyPhase

const (
	PhasePending    = securityv1alpha1.PolicyPhasePending
	PhaseChecking   = securityv1alpha1.PolicyPhaseChecking
	PhaseUpToDate   = securityv1alpha1.PolicyPhaseUpToDate
	PhaseBuilding   = securityv1alpha1.PolicyPhaseBuilding
	PhasePublishing = securityv1alpha1.PolicyPhasePublishing
	PhaseApplying   = securityv1alpha1.PolicyPhaseApplying
	PhaseRollingOut = securityv1alpha1.PolicyPhaseRollingOut
	PhaseSucceeded  = securityv1alpha1.PolicyPhaseSucceeded
	PhaseFailed     = securityv1alpha1.PolicyPhaseFailed
)

// SetPhase records the top-level status state for a policy reconcile run.
func SetPhase(policyStatus *securityv1alpha1.AltImageUpdatePolicyStatus, generation int64, phase Phase, reason, message string) bool {
	if policyStatus == nil {
		return false
	}

	changed := false
	if policyStatus.ObservedGeneration != generation {
		policyStatus.ObservedGeneration = generation
		changed = true
	}
	if policyStatus.Phase != phase {
		policyStatus.Phase = phase
		changed = true
	}
	if policyStatus.Reason != reason {
		policyStatus.Reason = reason
		changed = true
	}
	if policyStatus.Message != message {
		policyStatus.Message = message
		changed = true
	}
	return changed
}
