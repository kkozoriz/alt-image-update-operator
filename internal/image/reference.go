package image

import (
	"fmt"
	"strings"

	"github.com/distribution/reference"
)

// ParseOutputImage parses a build destination repository with an optional tag.
func ParseOutputImage(outputImage string) (reference.Named, error) {
	if strings.TrimSpace(outputImage) == "" {
		return nil, fmt.Errorf("invalid output image reference: value is empty")
	}

	ref, err := reference.Parse(outputImage)
	if err != nil {
		return nil, fmt.Errorf("invalid output image reference %q: %w", outputImage, err)
	}

	named, ok := ref.(reference.Named)
	if !ok {
		return nil, fmt.Errorf("invalid output image reference %q: repository name is required", outputImage)
	}

	if _, ok := ref.(reference.Digested); ok {
		return nil, fmt.Errorf("invalid output image reference %q: digest references are not supported for build destinations", outputImage)
	}

	return named, nil
}

// Repository returns the output image repository without any existing tag.
func Repository(outputImage string) (string, error) {
	named, err := ParseOutputImage(outputImage)
	if err != nil {
		return "", err
	}

	return reference.TrimNamed(named).String(), nil
}
