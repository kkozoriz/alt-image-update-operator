package deploy

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestObserveRolloutSucceededWhenObservedAndReplicasConverged(t *testing.T) {
	deployment := newRolloutDeployment(3, 7)
	deployment.Status.ObservedGeneration = 7
	deployment.Status.Replicas = 3
	deployment.Status.UpdatedReplicas = 3
	deployment.Status.AvailableReplicas = 3

	observation := mustObserveRollout(t, deployment, RolloutOptions{TargetGeneration: 7})

	if observation.State != RolloutStateSucceeded {
		t.Fatalf("state = %q, want %q", observation.State, RolloutStateSucceeded)
	}
	if observation.Reason != ReasonRolloutComplete {
		t.Fatalf("reason = %q, want %q", observation.Reason, ReasonRolloutComplete)
	}
	if observation.DesiredReplicas != 3 || observation.UpdatedReplicas != 3 || observation.AvailableReplicas != 3 {
		t.Fatalf("replica observation = desired %d updated %d available %d, want all 3", observation.DesiredReplicas, observation.UpdatedReplicas, observation.AvailableReplicas)
	}
}

func TestObserveRolloutSucceededForZeroReplicas(t *testing.T) {
	deployment := newRolloutDeployment(0, 12)
	deployment.Status.ObservedGeneration = 12

	observation := mustObserveRollout(t, deployment, RolloutOptions{TargetGeneration: 12})

	if observation.State != RolloutStateSucceeded {
		t.Fatalf("state = %q, want %q", observation.State, RolloutStateSucceeded)
	}
	if observation.DesiredReplicas != 0 {
		t.Fatalf("desired replicas = %d, want 0", observation.DesiredReplicas)
	}
}

func TestObserveRolloutProgressingWhenObservedGenerationIsBehind(t *testing.T) {
	deployment := newRolloutDeployment(2, 8)
	deployment.Status.ObservedGeneration = 7
	deployment.Status.Replicas = 2
	deployment.Status.UpdatedReplicas = 2
	deployment.Status.AvailableReplicas = 2

	observation := mustObserveRollout(t, deployment, RolloutOptions{TargetGeneration: 8})

	if observation.State != RolloutStateProgressing {
		t.Fatalf("state = %q, want %q", observation.State, RolloutStateProgressing)
	}
	if observation.Reason != ReasonDeploymentNotObserved {
		t.Fatalf("reason = %q, want %q", observation.Reason, ReasonDeploymentNotObserved)
	}
}

func TestObserveRolloutProgressingForPartialRollout(t *testing.T) {
	deployment := newRolloutDeployment(3, 4)
	deployment.Status.ObservedGeneration = 4
	deployment.Status.Replicas = 3
	deployment.Status.UpdatedReplicas = 2
	deployment.Status.AvailableReplicas = 1
	deployment.Status.UnavailableReplicas = 2

	observation := mustObserveRollout(t, deployment, RolloutOptions{TargetGeneration: 4})

	if observation.State != RolloutStateProgressing {
		t.Fatalf("state = %q, want %q", observation.State, RolloutStateProgressing)
	}
	if observation.Reason != ReasonRolloutReplicasNotReady {
		t.Fatalf("reason = %q, want %q", observation.Reason, ReasonRolloutReplicasNotReady)
	}
}

func TestObserveRolloutFailedOnProgressDeadlineExceeded(t *testing.T) {
	deployment := newRolloutDeployment(1, 5)
	deployment.Status.ObservedGeneration = 5
	deployment.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:    appsv1.DeploymentProgressing,
		Status:  corev1.ConditionFalse,
		Reason:  progressDeadlineExceededReason,
		Message: "ReplicaSet demo-app-abc has timed out progressing.",
	}}

	observation := mustObserveRollout(t, deployment, RolloutOptions{TargetGeneration: 5})

	if observation.State != RolloutStateFailed {
		t.Fatalf("state = %q, want %q", observation.State, RolloutStateFailed)
	}
	if observation.Reason != ReasonProgressDeadlineExceeded {
		t.Fatalf("reason = %q, want %q", observation.Reason, ReasonProgressDeadlineExceeded)
	}
	if observation.Message != "ReplicaSet demo-app-abc has timed out progressing." {
		t.Fatalf("message = %q, want condition message", observation.Message)
	}
}

func TestObserveRolloutFailedOnManualTimeout(t *testing.T) {
	startedAt := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	deployment := newRolloutDeployment(2, 3)
	deployment.Status.ObservedGeneration = 3
	deployment.Status.Replicas = 2
	deployment.Status.UpdatedReplicas = 1
	deployment.Status.AvailableReplicas = 1

	observation := mustObserveRollout(t, deployment, RolloutOptions{
		TargetGeneration: 3,
		StartedAt:        startedAt,
		Now:              startedAt.Add(10 * time.Minute),
		Timeout:          5 * time.Minute,
	})

	if observation.State != RolloutStateFailed {
		t.Fatalf("state = %q, want %q", observation.State, RolloutStateFailed)
	}
	if observation.Reason != ReasonRolloutTimedOut {
		t.Fatalf("reason = %q, want %q", observation.Reason, ReasonRolloutTimedOut)
	}
}

func TestObserveRolloutFailedOnReplicaFailureCondition(t *testing.T) {
	deployment := newRolloutDeployment(1, 5)
	deployment.Status.ObservedGeneration = 5
	deployment.Status.Conditions = []appsv1.DeploymentCondition{{
		Type:    appsv1.DeploymentReplicaFailure,
		Status:  corev1.ConditionTrue,
		Reason:  "FailedCreate",
		Message: "pods are forbidden",
	}}

	observation := mustObserveRollout(t, deployment, RolloutOptions{TargetGeneration: 5})

	if observation.State != RolloutStateFailed {
		t.Fatalf("state = %q, want %q", observation.State, RolloutStateFailed)
	}
	if observation.Reason != ReasonReplicaFailure {
		t.Fatalf("reason = %q, want %q", observation.Reason, ReasonReplicaFailure)
	}
}

func TestObserveRolloutRequiresDeploymentCurrentGenerationWhenTargetGenerationIsOlder(t *testing.T) {
	deployment := newRolloutDeployment(1, 9)
	deployment.Status.ObservedGeneration = 8
	deployment.Status.Replicas = 1
	deployment.Status.UpdatedReplicas = 1
	deployment.Status.AvailableReplicas = 1

	observation := mustObserveRollout(t, deployment, RolloutOptions{TargetGeneration: 7})

	if observation.State != RolloutStateProgressing {
		t.Fatalf("state = %q, want %q", observation.State, RolloutStateProgressing)
	}
	if observation.RequiredGeneration != 9 {
		t.Fatalf("required generation = %d, want 9", observation.RequiredGeneration)
	}
}

func TestObserveRolloutReturnsErrorForNilDeployment(t *testing.T) {
	if _, err := ObserveRollout(nil, RolloutOptions{}); err == nil {
		t.Fatal("ObserveRollout(nil) error = nil, want error")
	}
}

func mustObserveRollout(t *testing.T, deployment *appsv1.Deployment, options RolloutOptions) RolloutObservation {
	t.Helper()

	observation, err := ObserveRollout(deployment, options)
	if err != nil {
		t.Fatalf("ObserveRollout returned error: %v", err)
	}
	return observation
}

func newRolloutDeployment(replicas int32, generation int64) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "demo-app",
			Namespace:  "demo",
			Generation: generation,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
	}
}
