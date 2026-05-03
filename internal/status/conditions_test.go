package status

import (
	"testing"
	"time"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetConditionCreatesCondition(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC))
	var conditions []metav1.Condition

	changed := SetCondition(&conditions, 7, ConditionReady, metav1.ConditionFalse, "Checking", "check job is running", now)

	if !changed {
		t.Fatal("expected condition creation to report a change")
	}
	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}
	assertCondition(t, conditions[0], ConditionReady, metav1.ConditionFalse, "Checking", "check job is running", 7, now)
}

func TestSetConditionTransitionsCondition(t *testing.T) {
	createdAt := metav1.NewTime(time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC))
	transitionedAt := metav1.NewTime(time.Date(2026, 5, 2, 10, 5, 0, 0, time.UTC))
	conditions := []metav1.Condition{{
		Type:               ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: 7,
		LastTransitionTime: createdAt,
		Reason:             "Building",
		Message:            "build job is running",
	}}

	changed := SetCondition(&conditions, 7, ConditionReady, metav1.ConditionTrue, "Succeeded", "rollout completed", transitionedAt)

	if !changed {
		t.Fatal("expected condition transition to report a change")
	}
	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}
	assertCondition(t, conditions[0], ConditionReady, metav1.ConditionTrue, "Succeeded", "rollout completed", 7, transitionedAt)
}

func TestSetConditionDoesNotChangeNoopUpdate(t *testing.T) {
	createdAt := metav1.NewTime(time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC))
	later := metav1.NewTime(time.Date(2026, 5, 2, 10, 5, 0, 0, time.UTC))
	conditions := []metav1.Condition{{
		Type:               ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: 7,
		LastTransitionTime: createdAt,
		Reason:             "Building",
		Message:            "build job is running",
	}}

	changed := SetCondition(&conditions, 7, ConditionReady, metav1.ConditionFalse, "Building", "build job is running", later)

	if changed {
		t.Fatal("expected no-op update to report no change")
	}
	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}
	assertCondition(t, conditions[0], ConditionReady, metav1.ConditionFalse, "Building", "build job is running", 7, createdAt)
}

func TestSetConditionUpdatesObservedGenerationWithoutTransitionTimeChange(t *testing.T) {
	createdAt := metav1.NewTime(time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC))
	later := metav1.NewTime(time.Date(2026, 5, 2, 10, 5, 0, 0, time.UTC))
	conditions := []metav1.Condition{{
		Type:               ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: 7,
		LastTransitionTime: createdAt,
		Reason:             "Building",
		Message:            "build job is running",
	}}

	changed := SetCondition(&conditions, 8, ConditionReady, metav1.ConditionFalse, "Building", "build job is running", later)

	if !changed {
		t.Fatal("expected observedGeneration update to report a change")
	}
	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}
	assertCondition(t, conditions[0], ConditionReady, metav1.ConditionFalse, "Building", "build job is running", 8, createdAt)
}

func TestMarkFailedSetsPhaseAndFailureConditions(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC))
	policyStatus := &securityv1alpha1.AltImageUpdatePolicyStatus{}

	changed := MarkFailed(policyStatus, 9, now, "BuildJobFailed", "build job failed")

	if !changed {
		t.Fatal("expected failure mark to report a change")
	}
	if policyStatus.ObservedGeneration != 9 {
		t.Fatalf("expected observedGeneration 9, got %d", policyStatus.ObservedGeneration)
	}
	if policyStatus.Phase != securityv1alpha1.PolicyPhaseFailed {
		t.Fatalf("expected phase %q, got %q", securityv1alpha1.PolicyPhaseFailed, policyStatus.Phase)
	}
	if policyStatus.Reason != "BuildJobFailed" {
		t.Fatalf("expected reason BuildJobFailed, got %q", policyStatus.Reason)
	}
	if policyStatus.Message != "build job failed" {
		t.Fatalf("expected failure message, got %q", policyStatus.Message)
	}

	ready := findCondition(t, policyStatus.Conditions, ConditionReady)
	assertCondition(t, ready, ConditionReady, metav1.ConditionFalse, "BuildJobFailed", "build job failed", 9, now)
	failed := findCondition(t, policyStatus.Conditions, ConditionFailed)
	assertCondition(t, failed, ConditionFailed, metav1.ConditionTrue, "BuildJobFailed", "build job failed", 9, now)
}

func TestStageHelpersSetConsistentConditions(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC))
	tests := []struct {
		name          string
		mark          func(*securityv1alpha1.AltImageUpdatePolicyStatus, int64, metav1.Time, string, string) bool
		wantPhase     securityv1alpha1.PolicyPhase
		wantCondition string
		wantStatus    metav1.ConditionStatus
	}{
		{
			name:          "ready",
			mark:          MarkReady,
			wantPhase:     securityv1alpha1.PolicyPhaseSucceeded,
			wantCondition: ConditionReady,
			wantStatus:    metav1.ConditionTrue,
		},
		{
			name:          "checking",
			mark:          MarkChecking,
			wantPhase:     securityv1alpha1.PolicyPhaseChecking,
			wantCondition: ConditionCheckCompleted,
			wantStatus:    metav1.ConditionUnknown,
		},
		{
			name:          "building",
			mark:          MarkBuilding,
			wantPhase:     securityv1alpha1.PolicyPhaseBuilding,
			wantCondition: ConditionBuildCompleted,
			wantStatus:    metav1.ConditionUnknown,
		},
		{
			name:          "applying",
			mark:          MarkApplying,
			wantPhase:     securityv1alpha1.PolicyPhaseApplying,
			wantCondition: ConditionDeploymentUpdated,
			wantStatus:    metav1.ConditionUnknown,
		},
		{
			name:          "rollout",
			mark:          MarkRollout,
			wantPhase:     securityv1alpha1.PolicyPhaseRollingOut,
			wantCondition: ConditionRolloutCompleted,
			wantStatus:    metav1.ConditionUnknown,
		},
		{
			name:          "up-to-date",
			mark:          MarkUpToDate,
			wantPhase:     securityv1alpha1.PolicyPhaseUpToDate,
			wantCondition: ConditionUpToDate,
			wantStatus:    metav1.ConditionTrue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policyStatus := &securityv1alpha1.AltImageUpdatePolicyStatus{}

			changed := tt.mark(policyStatus, 12, now, "Reason", "message")

			if !changed {
				t.Fatal("expected stage helper to report a change")
			}
			if policyStatus.Phase != tt.wantPhase {
				t.Fatalf("expected phase %q, got %q", tt.wantPhase, policyStatus.Phase)
			}
			assertCondition(t, findCondition(t, policyStatus.Conditions, ConditionReady), ConditionReady, expectedReadyStatus(tt.name), "Reason", "message", 12, now)
			assertCondition(t, findCondition(t, policyStatus.Conditions, tt.wantCondition), tt.wantCondition, tt.wantStatus, "Reason", "message", 12, now)
		})
	}
}

func expectedReadyStatus(stage string) metav1.ConditionStatus {
	switch stage {
	case "ready", "up-to-date":
		return metav1.ConditionTrue
	default:
		return metav1.ConditionFalse
	}
}

func findCondition(t *testing.T, conditions []metav1.Condition, conditionType string) metav1.Condition {
	t.Helper()
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition
		}
	}
	t.Fatalf("condition %q not found in %#v", conditionType, conditions)
	return metav1.Condition{}
}

func assertCondition(t *testing.T, condition metav1.Condition, conditionType string, status metav1.ConditionStatus, reason, message string, generation int64, lastTransitionTime metav1.Time) {
	t.Helper()
	if condition.Type != conditionType {
		t.Fatalf("expected condition type %q, got %q", conditionType, condition.Type)
	}
	if condition.Status != status {
		t.Fatalf("expected condition status %q, got %q", status, condition.Status)
	}
	if condition.Reason != reason {
		t.Fatalf("expected reason %q, got %q", reason, condition.Reason)
	}
	if condition.Message != message {
		t.Fatalf("expected message %q, got %q", message, condition.Message)
	}
	if condition.ObservedGeneration != generation {
		t.Fatalf("expected observedGeneration %d, got %d", generation, condition.ObservedGeneration)
	}
	if !condition.LastTransitionTime.Equal(&lastTransitionTime) {
		t.Fatalf("expected lastTransitionTime %s, got %s", lastTransitionTime.Time, condition.LastTransitionTime.Time)
	}
}
