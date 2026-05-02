package jobs

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

const testRunKey = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestJobNamesAreDeterministicAndValid(t *testing.T) {
	first := CheckJobName("demo-policy", "g7-0123456789abcdef")
	second := CheckJobName("demo-policy", "g7-0123456789abcdef")

	if first != second {
		t.Fatalf("CheckJobName not deterministic: first %q second %q", first, second)
	}
	assertDNS1123Label(t, first)
	if len(first) > maxJobNameLength {
		t.Fatalf("CheckJobName length = %d, want <= %d", len(first), maxJobNameLength)
	}
	if !strings.HasPrefix(first, "aiuo-check-demo-policy-") {
		t.Fatalf("CheckJobName = %q, want check prefix and policy stem", first)
	}
}

func TestBuildAndCheckJobNamesDiffer(t *testing.T) {
	checkName := CheckJobName("demo-policy", "g7-0123456789abcdef")
	buildName := BuildJobName("demo-policy", "g7-0123456789abcdef")

	if checkName == buildName {
		t.Fatalf("check and build names are equal: %q", checkName)
	}
	assertDNS1123Label(t, checkName)
	assertDNS1123Label(t, buildName)
}

func TestLongPolicyNameIsTruncatedWithCollisionResistantHash(t *testing.T) {
	longNameA := "demo-" + strings.Repeat("very-long-policy-name-", 8) + "a"
	longNameB := "demo-" + strings.Repeat("very-long-policy-name-", 8) + "b"

	nameA := BuildJobName(longNameA, "g7-0123456789abcdef")
	nameB := BuildJobName(longNameB, "g7-0123456789abcdef")

	assertDNS1123Label(t, nameA)
	assertDNS1123Label(t, nameB)
	if len(nameA) > maxJobNameLength || len(nameB) > maxJobNameLength {
		t.Fatalf("long policy names produced oversized names: %q (%d), %q (%d)", nameA, len(nameA), nameB, len(nameB))
	}
	if nameA == nameB {
		t.Fatalf("different long policy names collided: %q", nameA)
	}
	if commonPrefixBeforeHash(nameA) != commonPrefixBeforeHash(nameB) {
		t.Fatalf("expected same truncated stem before hash: %q vs %q", nameA, nameB)
	}
}

func TestJobNameChangesWhenBuildIDChanges(t *testing.T) {
	first := BuildJobName("demo-policy", "g7-0123456789abcdef")
	second := BuildJobName("demo-policy", "g8-fedcba9876543210")

	if first == second {
		t.Fatalf("BuildJobName did not change after build ID changed: %q", first)
	}
}

func TestLabelsContainLookupInputs(t *testing.T) {
	labels := Labels("demo-policy", "demo", testRunKey, "g7-0123456789abcdef", JobTypeBuild)

	want := map[string]string{
		LabelManagedBy:       operatorName,
		LabelPartOf:          operatorName,
		LabelPolicyName:      "demo-policy",
		LabelPolicyNamespace: "demo",
		LabelRunKey:          runKeyLabelValue(testRunKey),
		LabelBuildID:         "g7-0123456789abcdef",
		LabelJobType:         "build",
	}
	for key, value := range want {
		if labels[key] != value {
			t.Fatalf("Labels()[%q] = %q, want %q", key, labels[key], value)
		}
	}
	assertValidLabels(t, labels)
}

func TestLookupLabelsAreSubsetOfJobLabels(t *testing.T) {
	labels := Labels("demo-policy", "demo", testRunKey, "g7-0123456789abcdef", JobTypeCheck)
	lookupLabels := LookupLabels("demo-policy", "demo", testRunKey, JobTypeCheck)

	for key, value := range lookupLabels {
		if labels[key] != value {
			t.Fatalf("lookup label %q = %q, job labels have %q", key, value, labels[key])
		}
	}
	if _, ok := lookupLabels[LabelBuildID]; ok {
		t.Fatalf("LookupLabels unexpectedly includes build-id label")
	}
	assertValidLabels(t, lookupLabels)
}

func TestLabelsForLongValuesRemainValidAndAnnotationsKeepExactValues(t *testing.T) {
	longPolicyName := "demo-" + strings.Repeat("very-long-policy-name-", 10)
	namespace := "security-team"
	runKey := "sha256:" + strings.Repeat("abcdef0123456789", 4)
	buildID := "g12-" + strings.Repeat("abcdef", 16)

	labels := Labels(longPolicyName, namespace, runKey, buildID, JobTypeBuild)
	annotations := Annotations(longPolicyName, namespace, runKey, buildID, JobTypeBuild)

	assertValidLabels(t, labels)
	for key, value := range labels {
		if len(value) > maxLabelValueBytes {
			t.Fatalf("label %q value length = %d, want <= %d", key, len(value), maxLabelValueBytes)
		}
	}
	if annotations[AnnotationPolicyName] != longPolicyName {
		t.Fatalf("annotation policy name lost exact value")
	}
	if annotations[AnnotationRunKey] != runKey {
		t.Fatalf("annotation run key lost exact value")
	}
	if annotations[AnnotationBuildID] != buildID {
		t.Fatalf("annotation build id lost exact value")
	}
}

func assertDNS1123Label(t *testing.T, value string) {
	t.Helper()
	if errs := validation.IsDNS1123Label(value); len(errs) > 0 {
		t.Fatalf("%q is not a DNS-1123 label: %v", value, errs)
	}
}

func assertValidLabels(t *testing.T, labels map[string]string) {
	t.Helper()
	for key, value := range labels {
		if errs := validation.IsQualifiedName(key); len(errs) > 0 {
			t.Fatalf("label key %q is invalid: %v", key, errs)
		}
		if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
			t.Fatalf("label %q value %q is invalid: %v", key, value, errs)
		}
	}
}

func commonPrefixBeforeHash(value string) string {
	index := strings.LastIndex(value, "-")
	if index == -1 {
		return value
	}
	return value[:index]
}
