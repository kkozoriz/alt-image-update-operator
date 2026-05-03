package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

const (
	operatorName = "alt-image-update-operator"

	maxJobNameLength   = 63
	nameHashLength     = 12
	labelHashLength    = 16
	maxLabelValueBytes = 63

	LabelManagedBy       = "app.kubernetes.io/managed-by"
	LabelPartOf          = "app.kubernetes.io/part-of"
	LabelPolicyName      = "security.altlinux.org/policy-name"
	LabelPolicyNamespace = "security.altlinux.org/policy-namespace"
	LabelRunKey          = "security.altlinux.org/run-key"
	LabelBuildID         = "security.altlinux.org/build-id"
	LabelJobType         = "security.altlinux.org/job-type"

	AnnotationPolicyName      = "security.altlinux.org/policy-name"
	AnnotationPolicyNamespace = "security.altlinux.org/policy-namespace"
	AnnotationRunKey          = "security.altlinux.org/run-key"
	AnnotationBuildID         = "security.altlinux.org/build-id"
	AnnotationJobType         = "security.altlinux.org/job-type"
)

type JobType string

const (
	JobTypeCheck JobType = "check"
	JobTypeBuild JobType = "build"
)

var invalidDNSLabelChar = regexp.MustCompile(`[^a-z0-9-]+`)
var invalidLabelValueChar = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// CheckJobName returns the deterministic Kubernetes Job name for an apt simulation check run.
func CheckJobName(policyName, buildID string) string {
	return JobName(policyName, buildID, JobTypeCheck)
}

// BuildJobName returns the deterministic Kubernetes Job name for an image build run.
func BuildJobName(policyName, buildID string) string {
	return JobName(policyName, buildID, JobTypeBuild)
}

// JobName returns a deterministic DNS-1123 label that is short enough for generated Job pods.
func JobName(policyName, buildID string, jobType JobType) string {
	normalizedType := normalizeDNSLabel(string(jobType), "job")
	prefix := "aiuo-" + normalizedType + "-"
	hash := shortHash(fmt.Sprintf("%s\n%s\n%s", policyName, buildID, jobType), nameHashLength)
	stemBudget := maxJobNameLength - len(prefix) - 1 - len(hash)
	if stemBudget < 1 {
		stemBudget = 1
	}

	stem := truncateDNSLabel(normalizeDNSLabel(policyName, "policy"), stemBudget)
	return prefix + stem + "-" + hash
}

// Labels returns Kubernetes labels shared by check and build Jobs for idempotent lookup.
func Labels(policyName, policyNamespace, runKey, buildID string, jobType JobType) map[string]string {
	labels := LookupLabels(policyName, policyNamespace, runKey, jobType)
	labels[LabelBuildID] = safeLabelValue(buildID, "build")
	return labels
}

// LookupLabels returns the stable label set the controller can use to find an existing Job for a run.
func LookupLabels(policyName, policyNamespace, runKey string, jobType JobType) map[string]string {
	return map[string]string{
		LabelManagedBy:       operatorName,
		LabelPartOf:          operatorName,
		LabelPolicyName:      safeLabelValue(policyName, "policy"),
		LabelPolicyNamespace: safeLabelValue(policyNamespace, "namespace"),
		LabelRunKey:          runKeyLabelValue(runKey),
		LabelJobType:         safeLabelValue(string(jobType), "job"),
	}
}

// Annotations returns exact metadata values that may be too long or contain characters invalid in labels.
func Annotations(policyName, policyNamespace, runKey, buildID string, jobType JobType) map[string]string {
	return map[string]string{
		AnnotationPolicyName:      policyName,
		AnnotationPolicyNamespace: policyNamespace,
		AnnotationRunKey:          runKey,
		AnnotationBuildID:         buildID,
		AnnotationJobType:         string(jobType),
	}
}

func normalizeDNSLabel(value, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = invalidDNSLabelChar.ReplaceAllString(normalized, "-")
	normalized = strings.Trim(normalized, "-")
	if normalized == "" {
		return fallback
	}
	return normalized
}

func truncateDNSLabel(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	return strings.Trim(value[:maxLength], "-")
}

func safeLabelValue(value, fallback string) string {
	normalized := strings.TrimSpace(value)
	normalized = invalidLabelValueChar.ReplaceAllString(normalized, "-")
	normalized = strings.Trim(normalized, "-_.")
	if normalized == "" {
		normalized = fallback
	}
	if len(normalized) <= maxLabelValueBytes {
		return normalized
	}

	hash := shortHash(value, labelHashLength)
	budget := maxLabelValueBytes - len(hash) - 1
	if budget < 1 {
		return hash[:maxLabelValueBytes]
	}

	return strings.Trim(normalized[:budget], "-_.") + "-" + hash
}

func runKeyLabelValue(runKey string) string {
	return "rk-" + shortHash(runKey, labelHashLength)
}

func shortHash(value string, length int) string {
	sum := sha256.Sum256([]byte(value))
	encoded := hex.EncodeToString(sum[:])
	if length > len(encoded) {
		length = len(encoded)
	}
	return encoded[:length]
}
