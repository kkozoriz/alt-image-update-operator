package deploy

import (
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
)

const (
	ReasonTargetContainerNotFound = "TargetContainerNotFound"

	AnnotationLastBuildID     = "security.altlinux.org/last-build-id"
	AnnotationLastBuiltImage  = "security.altlinux.org/last-built-image"
	AnnotationLastAppliedAt   = "security.altlinux.org/last-applied-at"
	AnnotationPolicyName      = "security.altlinux.org/policy-name"
	AnnotationPolicyNamespace = "security.altlinux.org/policy-namespace"
)

var ErrTargetContainerNotFound = errors.New(ReasonTargetContainerNotFound)

// ImagePatch describes the desired Deployment pod template image and metadata.
type ImagePatch struct {
	ContainerName   string
	Image           string
	BuildID         string
	AppliedAt       string
	PolicyName      string
	PolicyNamespace string
}

// RequiredAnnotations returns the pod template annotations written with an image patch.
func (p ImagePatch) RequiredAnnotations() map[string]string {
	return map[string]string{
		AnnotationLastBuildID:     p.BuildID,
		AnnotationLastBuiltImage:  p.Image,
		AnnotationLastAppliedAt:   p.AppliedAt,
		AnnotationPolicyName:      p.PolicyName,
		AnnotationPolicyNamespace: p.PolicyNamespace,
	}
}

// ApplyImagePatch updates only the named regular container image and required pod template annotations.
func ApplyImagePatch(deployment *appsv1.Deployment, patch ImagePatch) (bool, error) {
	if deployment == nil {
		return false, errors.New("deployment is nil")
	}

	containerIndex := -1
	for i := range deployment.Spec.Template.Spec.Containers {
		if deployment.Spec.Template.Spec.Containers[i].Name == patch.ContainerName {
			containerIndex = i
			break
		}
	}
	if containerIndex == -1 {
		return false, fmt.Errorf("%w: deployment %q container %q", ErrTargetContainerNotFound, deployment.Name, patch.ContainerName)
	}

	changed := false
	if deployment.Spec.Template.Spec.Containers[containerIndex].Image != patch.Image {
		deployment.Spec.Template.Spec.Containers[containerIndex].Image = patch.Image
		changed = true
	}

	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	for key, value := range patch.RequiredAnnotations() {
		if deployment.Spec.Template.Annotations[key] == value {
			continue
		}
		deployment.Spec.Template.Annotations[key] = value
		changed = true
	}

	return changed, nil
}
