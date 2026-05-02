package run

import (
	"regexp"
	"testing"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRunKeyAndBuildIDAreStable(t *testing.T) {
	policy := newPolicy()

	firstRunKey := RunKey(policy)
	firstBuildID := BuildID(policy)

	secondRunKey := RunKey(policy.DeepCopy())
	secondBuildID := BuildID(policy.DeepCopy())

	if firstRunKey != secondRunKey {
		t.Fatalf("RunKey() not stable: first %q second %q", firstRunKey, secondRunKey)
	}
	if firstBuildID != secondBuildID {
		t.Fatalf("BuildID() not stable: first %q second %q", firstBuildID, secondBuildID)
	}
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(firstRunKey) {
		t.Fatalf("RunKey() = %q, want sha256 hex key", firstRunKey)
	}
	if !regexp.MustCompile(`^g7-[0-9a-f]{16}$`).MatchString(firstBuildID) {
		t.Fatalf("BuildID() = %q, want deterministic tag-safe id", firstBuildID)
	}
}

func TestManualTokenChangesRunKeyAndBuildID(t *testing.T) {
	policy := newPolicy()
	originalRunKey := RunKey(policy)
	originalBuildID := BuildID(policy)

	policy.Spec.Trigger.ManualToken = "second-run"

	if got := RunKey(policy); got == originalRunKey {
		t.Fatalf("RunKey() did not change after manual token update: %q", got)
	}
	if got := BuildID(policy); got == originalBuildID {
		t.Fatalf("BuildID() did not change after manual token update: %q", got)
	}
}

func TestStatusDoesNotAffectRunKeyOrBuildID(t *testing.T) {
	policy := newPolicy()
	originalRunKey := RunKey(policy)
	originalBuildID := BuildID(policy)

	policy.Status.CurrentRunKey = "sha256:previous"
	policy.Status.BuildID = "g7-previous"
	policy.Status.LastBuiltImage = "registry.local/demo/app:older"
	policy.Status.LastAppliedImage = "registry.local/demo/app:older"
	policy.Status.Phase = securityv1alpha1.PolicyPhaseSucceeded
	policy.Status.ObservedGeneration = policy.Generation
	policy.Status.Conditions = []metav1.Condition{{
		Type:               securityv1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: policy.Generation,
		Reason:             "Complete",
		Message:            "workflow completed",
	}}

	if got := RunKey(policy); got != originalRunKey {
		t.Fatalf("RunKey() changed after status update: got %q want %q", got, originalRunKey)
	}
	if got := BuildID(policy); got != originalBuildID {
		t.Fatalf("BuildID() changed after status update: got %q want %q", got, originalBuildID)
	}
}

func TestGenerationOnlyRetriggerChangesRunKeyAndBuildID(t *testing.T) {
	policy := newPolicy()
	policy.Spec.Trigger.ManualToken = ""
	originalRunKey := RunKey(policy)
	originalBuildID := BuildID(policy)

	policy.Generation++

	if got := RunKey(policy); got == originalRunKey {
		t.Fatalf("RunKey() did not change after generation update: %q", got)
	}
	if got := BuildID(policy); got == originalBuildID {
		t.Fatalf("BuildID() did not change after generation update: %q", got)
	}
}

func TestRunKeyChangesWhenIdentityInputsChange(t *testing.T) {
	policy := newPolicy()
	originalRunKey := RunKey(policy)

	cases := map[string]func(*securityv1alpha1.AltImageUpdatePolicy){
		"target name": func(p *securityv1alpha1.AltImageUpdatePolicy) { p.Spec.TargetRef.Name = "other-app" },
		"container":   func(p *securityv1alpha1.AltImageUpdatePolicy) { p.Spec.ContainerName = "worker" },
		"context name": func(p *securityv1alpha1.AltImageUpdatePolicy) {
			p.Spec.Build.Context.ConfigMapRef.Name = "other-context"
		},
		"dockerfile key": func(p *securityv1alpha1.AltImageUpdatePolicy) {
			p.Spec.Build.Context.ConfigMapRef.DockerfileKey = "Containerfile"
		},
		"output image": func(p *securityv1alpha1.AltImageUpdatePolicy) { p.Spec.Build.OutputImage = "registry.local/demo/other" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := policy.DeepCopy()
			mutate(candidate)
			if got := RunKey(candidate); got == originalRunKey {
				t.Fatalf("RunKey() did not change after %s update: %q", name, got)
			}
		})
	}
}

func newPolicy() *securityv1alpha1.AltImageUpdatePolicy {
	return &securityv1alpha1.AltImageUpdatePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "demo-policy",
			Namespace:  "demo",
			Generation: 7,
		},
		Spec: securityv1alpha1.AltImageUpdatePolicySpec{
			Trigger: securityv1alpha1.TriggerSpec{
				ManualToken: "first-run",
			},
			Build: securityv1alpha1.BuildSpec{
				OutputImage: "registry.local:5000/demo/app:latest",
				TagTemplate: "alt-{{ .BuildID }}",
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
