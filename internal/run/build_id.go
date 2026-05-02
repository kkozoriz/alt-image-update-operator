package run

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"
)

const (
	runKeyHashBytes  = 32
	buildIDHashChars = 16
)

// Inputs contains the policy fields that define one idempotent reconcile run.
type Inputs struct {
	Generation int64 `json:"generation"`

	ManualToken string `json:"manualToken,omitempty"`

	TargetAPIVersion string `json:"targetAPIVersion"`
	TargetKind       string `json:"targetKind"`
	TargetName       string `json:"targetName"`
	ContainerName    string `json:"containerName"`

	ContextType      string `json:"contextType"`
	ContextName      string `json:"contextName"`
	DockerfileKey    string `json:"dockerfileKey"`
	OutputImage      string `json:"outputImage"`
	OutputTagPattern string `json:"outputTagPattern,omitempty"`
}

// InputsFromPolicy extracts the stable spec and metadata fields used for run identity.
func InputsFromPolicy(policy *securityv1alpha1.AltImageUpdatePolicy) Inputs {
	if policy == nil {
		return Inputs{}
	}

	return Inputs{
		Generation:       policy.Generation,
		ManualToken:      policy.Spec.Trigger.ManualToken,
		TargetAPIVersion: policy.Spec.TargetRef.APIVersion,
		TargetKind:       policy.Spec.TargetRef.Kind,
		TargetName:       policy.Spec.TargetRef.Name,
		ContainerName:    policy.Spec.ContainerName,
		ContextType:      string(policy.Spec.Build.Context.Type),
		ContextName:      policy.Spec.Build.Context.ConfigMapRef.Name,
		DockerfileKey:    policy.Spec.Build.Context.ConfigMapRef.DockerfileKey,
		OutputImage:      policy.Spec.Build.OutputImage,
		OutputTagPattern: policy.Spec.Build.TagTemplate,
	}
}

// RunKey returns a stable key for one policy run.
func RunKey(policy *securityv1alpha1.AltImageUpdatePolicy) string {
	return RunKeyForInputs(InputsFromPolicy(policy))
}

// RunKeyForInputs returns a stable key for one set of policy run inputs.
func RunKeyForInputs(inputs Inputs) string {
	sum := sha256.Sum256(mustCanonicalJSON(inputs))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// BuildID returns a deterministic, tag-safe identifier for one policy run.
func BuildID(policy *securityv1alpha1.AltImageUpdatePolicy) string {
	return BuildIDForInputs(InputsFromPolicy(policy))
}

// BuildIDForInputs returns a deterministic, tag-safe identifier for one set of inputs.
func BuildIDForInputs(inputs Inputs) string {
	runKey := RunKeyForInputs(inputs)
	return BuildIDForRunKey(inputs.Generation, runKey)
}

// BuildIDForRunKey returns a compact identifier from a run key and generation.
func BuildIDForRunKey(generation int64, runKey string) string {
	hexHash := strings.TrimPrefix(runKey, "sha256:")
	if len(hexHash) < buildIDHashChars || !isHex(hexHash) {
		sum := sha256.Sum256([]byte(runKey))
		hexHash = hex.EncodeToString(sum[:])
	}

	if generation < 0 {
		generation = 0
	}
	return fmt.Sprintf("g%d-%s", generation, hexHash[:buildIDHashChars])
}

func mustCanonicalJSON(inputs Inputs) []byte {
	data, err := json.Marshal(inputs)
	if err != nil {
		panic(fmt.Sprintf("marshal run inputs: %v", err))
	}
	return data
}

func isHex(value string) bool {
	if len(value) != runKeyHashBytes*2 {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}
