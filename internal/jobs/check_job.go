package jobs

import (
	"fmt"
	"strings"

	securityv1alpha1 "alt-image-update-operator/api/v1alpha1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	CheckContainerName = "alt-apt-simulation"

	checkCommand = "apt-get update && apt-get -s dist-upgrade"
)

// CheckJobOptions contains controller-computed values for one check run.
type CheckJobOptions struct {
	RunKey  string
	BuildID string
}

// NewCheckJob creates a real Kubernetes Job that checks ALT package updates with apt simulation.
func NewCheckJob(policy *securityv1alpha1.AltImageUpdatePolicy, opts CheckJobOptions) (*batchv1.Job, error) {
	if policy == nil {
		return nil, fmt.Errorf("check job policy is nil")
	}
	if policy.Spec.Check.Mode != securityv1alpha1.CheckModeAltAptSimulation {
		return nil, fmt.Errorf("unsupported check mode %q", policy.Spec.Check.Mode)
	}
	if strings.TrimSpace(opts.RunKey) == "" {
		return nil, fmt.Errorf("check job run key is required")
	}
	if strings.TrimSpace(opts.BuildID) == "" {
		return nil, fmt.Errorf("check job build id is required")
	}
	if strings.TrimSpace(policy.Spec.Alt.BaseImage) == "" {
		return nil, fmt.Errorf("ALT base image is required")
	}

	labels := Labels(policy.Name, policy.Namespace, opts.RunKey, opts.BuildID, JobTypeCheck)
	annotations := Annotations(policy.Name, policy.Namespace, opts.RunKey, opts.BuildID, JobTypeCheck)

	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            CheckJobName(policy.Name, opts.BuildID),
			Namespace:       policy.Namespace,
			Labels:          labels,
			Annotations:     annotations,
			OwnerReferences: ownerReferences(policy),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            int32Ptr(jobBackoffLimit(policy)),
			TTLSecondsAfterFinished: int32Ptr(jobTTLSeconds(policy)),
			ActiveDeadlineSeconds:   int64Ptr(jobActiveDeadlineSeconds(policy)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: jobServiceAccountName(policy),
					HostNetwork:        jobHostNetwork(policy),
					DNSPolicy:          jobDNSPolicy(policy),
					RestartPolicy:      corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:            CheckContainerName,
							Image:           policy.Spec.Alt.BaseImage,
							Command:         []string{"/bin/sh", "-ec"},
							Args:            []string{checkCommand},
							Env:             checkEnv(policy),
							Resources:       defaultCheckResources(),
							SecurityContext: checkContainerSecurityContext(),
						},
					},
				},
			},
		},
	}

	return job, nil
}

func checkEnv(policy *securityv1alpha1.AltImageUpdatePolicy) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  "ALT_BRANCH",
			Value: string(policy.Spec.Alt.Branch),
		},
		{
			Name: "POLICY_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.labels['" + LabelPolicyName + "']",
				},
			},
		},
	}
}

func defaultCheckResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
}

func checkContainerSecurityContext() *corev1.SecurityContext {
	allowPrivilegeEscalation := false
	privileged := false
	readOnlyRootFilesystem := false

	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: &allowPrivilegeEscalation,
		Privileged:               &privileged,
		ReadOnlyRootFilesystem:   &readOnlyRootFilesystem,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
}
