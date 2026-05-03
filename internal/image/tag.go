package image

import (
	"fmt"
	"strings"

	"github.com/distribution/reference"
)

// BuildTag validates a deterministic build id for use as an image tag.
func BuildTag(buildID string) (string, error) {
	if strings.TrimSpace(buildID) == "" {
		return "", fmt.Errorf("invalid build id for image tag: value is empty")
	}

	repo, err := reference.WithName("example.com/repository")
	if err != nil {
		return "", fmt.Errorf("create tag validation repository: %w", err)
	}
	if _, err := reference.WithTag(repo, buildID); err != nil {
		return "", fmt.Errorf("invalid build id %q for image tag: %w", buildID, err)
	}

	return buildID, nil
}

// BuildReference replaces any output image tag with the deterministic build tag.
func BuildReference(outputImage, buildID string) (string, error) {
	named, err := ParseOutputImage(outputImage)
	if err != nil {
		return "", err
	}

	tag, err := BuildTag(buildID)
	if err != nil {
		return "", err
	}

	tagged, err := reference.WithTag(reference.TrimNamed(named), tag)
	if err != nil {
		return "", fmt.Errorf("construct build image reference for %q: %w", outputImage, err)
	}

	return tagged.String(), nil
}
