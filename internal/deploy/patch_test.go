package deploy

import (
	"errors"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyImagePatchChangesOnlyTargetContainerAndAnnotations(t *testing.T) {
	deployment := newDeployment(map[string]string{"existing": "keep"})
	patch := newImagePatch()

	changed, err := ApplyImagePatch(deployment, patch)
	if err != nil {
		t.Fatalf("ApplyImagePatch returned error: %v", err)
	}
	if !changed {
		t.Fatal("ApplyImagePatch changed = false, want true")
	}

	containers := deployment.Spec.Template.Spec.Containers
	if containers[0].Image != patch.Image {
		t.Fatalf("target container image = %q, want %q", containers[0].Image, patch.Image)
	}
	if containers[1].Image != "registry.local/demo/sidecar:v1" {
		t.Fatalf("sidecar image changed to %q", containers[1].Image)
	}
	if deployment.Spec.Template.Spec.InitContainers[0].Image != "registry.local/demo/init:v1" {
		t.Fatalf("init container image changed to %q", deployment.Spec.Template.Spec.InitContainers[0].Image)
	}

	annotations := deployment.Spec.Template.Annotations
	if annotations["existing"] != "keep" {
		t.Fatalf("existing annotation = %q, want keep", annotations["existing"])
	}
	for key, want := range patch.RequiredAnnotations() {
		if got := annotations[key]; got != want {
			t.Fatalf("annotation %s = %q, want %q", key, got, want)
		}
	}
}

func TestApplyImagePatchReturnsTargetContainerNotFoundWithoutMutating(t *testing.T) {
	deployment := newDeployment(map[string]string{"existing": "keep"})
	original := deployment.DeepCopy()
	patch := newImagePatch()
	patch.ContainerName = "missing"

	changed, err := ApplyImagePatch(deployment, patch)
	if !errors.Is(err, ErrTargetContainerNotFound) {
		t.Fatalf("ApplyImagePatch error = %v, want ErrTargetContainerNotFound", err)
	}
	if changed {
		t.Fatal("ApplyImagePatch changed = true, want false")
	}
	if !reflect.DeepEqual(deployment, original) {
		t.Fatal("ApplyImagePatch mutated Deployment after target container was not found")
	}
}

func TestApplyImagePatchNoopWhenImageAndAnnotationsAlreadyApplied(t *testing.T) {
	patch := newImagePatch()
	deployment := newDeployment(patch.RequiredAnnotations())
	deployment.Spec.Template.Spec.Containers[0].Image = patch.Image
	original := deployment.DeepCopy()

	changed, err := ApplyImagePatch(deployment, patch)
	if err != nil {
		t.Fatalf("ApplyImagePatch returned error: %v", err)
	}
	if changed {
		t.Fatal("ApplyImagePatch changed = true, want false")
	}
	if !reflect.DeepEqual(deployment, original) {
		t.Fatal("ApplyImagePatch mutated Deployment for no-op input")
	}
}

func TestApplyImagePatchUpdatesAnnotationsWhenImageAlreadyApplied(t *testing.T) {
	patch := newImagePatch()
	deployment := newDeployment(map[string]string{
		AnnotationLastBuildID: "old-build",
	})
	deployment.Spec.Template.Spec.Containers[0].Image = patch.Image

	changed, err := ApplyImagePatch(deployment, patch)
	if err != nil {
		t.Fatalf("ApplyImagePatch returned error: %v", err)
	}
	if !changed {
		t.Fatal("ApplyImagePatch changed = false, want true")
	}
	for key, want := range patch.RequiredAnnotations() {
		if got := deployment.Spec.Template.Annotations[key]; got != want {
			t.Fatalf("annotation %s = %q, want %q", key, got, want)
		}
	}
}

func TestApplyImagePatchCreatesPodTemplateAnnotationsMap(t *testing.T) {
	patch := newImagePatch()
	deployment := newDeployment(nil)

	changed, err := ApplyImagePatch(deployment, patch)
	if err != nil {
		t.Fatalf("ApplyImagePatch returned error: %v", err)
	}
	if !changed {
		t.Fatal("ApplyImagePatch changed = false, want true")
	}
	if deployment.Spec.Template.Annotations == nil {
		t.Fatal("pod template annotations map is nil")
	}
}

func newDeployment(annotations map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-app",
			Namespace: "demo",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{{
						Name:  "app",
						Image: "registry.local/demo/init:v1",
					}},
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "registry.local/demo/app:v1",
						},
						{
							Name:  "sidecar",
							Image: "registry.local/demo/sidecar:v1",
						},
					},
				},
			},
		},
	}
}

func newImagePatch() ImagePatch {
	return ImagePatch{
		ContainerName:   "app",
		Image:           "registry.local/demo/app:g7-0123456789abcdef",
		BuildID:         "g7-0123456789abcdef",
		AppliedAt:       "2026-05-02T12:15:00Z",
		PolicyName:      "demo-policy",
		PolicyNamespace: "demo",
	}
}
