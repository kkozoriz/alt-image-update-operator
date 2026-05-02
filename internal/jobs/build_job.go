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
	DefaultKanikoExecutorImage = "gcr.io/kaniko-project/executor:latest"

	DefaultJobServiceAccountName    = "default"
	DefaultJobTTLSeconds            = int32(600)
	DefaultJobBackoffLimit          = int32(0)
	DefaultJobActiveDeadlineSeconds = int64(900)

	BuildContainerName = "kaniko"

	buildContextVolumeName = "build-context"
	buildResultsVolumeName = "kaniko-results"
	dockerConfigVolumeName = "kaniko-docker-config"

	WorkspaceMountPath    = "/workspace"
	ResultsMountPath      = "/results"
	DockerConfigMountPath = "/kaniko/.docker"
)

// BuildJobOptions contains controller-computed values for one build run.
type BuildJobOptions struct {
	RunKey     string
	BuildID    string
	BuiltImage string
}

// NewBuildJob creates a real Kaniko Kubernetes Job for the supplied policy run.
func NewBuildJob(policy *securityv1alpha1.AltImageUpdatePolicy, opts BuildJobOptions) (*batchv1.Job, error) {
	if policy == nil {
		return nil, fmt.Errorf("build job policy is nil")
	}
	if policy.Spec.Build.Builder != securityv1alpha1.BuilderTypeKaniko {
		return nil, fmt.Errorf("unsupported build builder %q", policy.Spec.Build.Builder)
	}
	if policy.Spec.Build.Context.Type != securityv1alpha1.BuildContextTypeConfigMap {
		return nil, fmt.Errorf("unsupported build context type %q", policy.Spec.Build.Context.Type)
	}
	if strings.TrimSpace(opts.RunKey) == "" {
		return nil, fmt.Errorf("build job run key is required")
	}
	if strings.TrimSpace(opts.BuildID) == "" {
		return nil, fmt.Errorf("build job build id is required")
	}
	if strings.TrimSpace(opts.BuiltImage) == "" {
		return nil, fmt.Errorf("build job destination image is required")
	}

	contextRef := policy.Spec.Build.Context.ConfigMapRef
	if strings.TrimSpace(contextRef.Name) == "" {
		return nil, fmt.Errorf("build context ConfigMap name is required")
	}
	if strings.TrimSpace(contextRef.DockerfileKey) == "" {
		return nil, fmt.Errorf("build context dockerfile key is required")
	}
	if policy.Spec.Build.RegistrySecretRef != nil && strings.TrimSpace(policy.Spec.Build.RegistrySecretRef.Name) == "" {
		return nil, fmt.Errorf("registry secret name is required when registrySecretRef is specified")
	}

	labels := Labels(policy.Name, policy.Namespace, opts.RunKey, opts.BuildID, JobTypeBuild)
	annotations := Annotations(policy.Name, policy.Namespace, opts.RunKey, opts.BuildID, JobTypeBuild)

	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            BuildJobName(policy.Name, opts.BuildID),
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
					RestartPolicy:      corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:         BuildContainerName,
							Image:        kanikoBuilderImage(policy),
							Args:         kanikoArgs(contextRef.DockerfileKey, opts.BuiltImage),
							Env:          buildEnv(policy),
							VolumeMounts: buildVolumeMounts(policy),
							Resources:    defaultBuildResources(),
						},
					},
					Volumes: buildVolumes(policy),
				},
			},
		},
	}

	return job, nil
}

func kanikoBuilderImage(policy *securityv1alpha1.AltImageUpdatePolicy) string {
	if image := strings.TrimSpace(policy.Spec.Build.BuilderImage); image != "" {
		return image
	}
	return DefaultKanikoExecutorImage
}

func kanikoArgs(dockerfileKey, builtImage string) []string {
	return []string{
		"--context=dir://" + WorkspaceMountPath,
		"--dockerfile=" + WorkspaceMountPath + "/" + dockerfileKey,
		"--destination=" + builtImage,
		"--digest-file=" + ResultsMountPath + "/image-digest",
		"--build-arg=ALT_BASE_IMAGE=$(ALT_BASE_IMAGE)",
		"--build-arg=ALT_BRANCH=$(ALT_BRANCH)",
	}
}

func buildEnv(policy *securityv1alpha1.AltImageUpdatePolicy) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  "ALT_BRANCH",
			Value: string(policy.Spec.Alt.Branch),
		},
		{
			Name:  "ALT_BASE_IMAGE",
			Value: policy.Spec.Alt.BaseImage,
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

func buildVolumes(policy *securityv1alpha1.AltImageUpdatePolicy) []corev1.Volume {
	volumes := []corev1.Volume{
		{
			Name: buildContextVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: policy.Spec.Build.Context.ConfigMapRef.Name,
					},
				},
			},
		},
		{
			Name: buildResultsVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	if policy.Spec.Build.RegistrySecretRef != nil {
		volumes = append(volumes, corev1.Volume{
			Name: dockerConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: policy.Spec.Build.RegistrySecretRef.Name,
					Items: []corev1.KeyToPath{
						{
							Key:  corev1.DockerConfigJsonKey,
							Path: "config.json",
						},
					},
				},
			},
		})
	}

	return volumes
}

func buildVolumeMounts(policy *securityv1alpha1.AltImageUpdatePolicy) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{
		{
			Name:      buildContextVolumeName,
			MountPath: WorkspaceMountPath,
			ReadOnly:  true,
		},
		{
			Name:      buildResultsVolumeName,
			MountPath: ResultsMountPath,
		},
	}

	if policy.Spec.Build.RegistrySecretRef != nil {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      dockerConfigVolumeName,
			MountPath: DockerConfigMountPath,
			ReadOnly:  true,
		})
	}

	return mounts
}

func defaultBuildResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("250m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
	}
}

func jobServiceAccountName(policy *securityv1alpha1.AltImageUpdatePolicy) string {
	if serviceAccountName := strings.TrimSpace(policy.Spec.JobTemplate.ServiceAccountName); serviceAccountName != "" {
		return serviceAccountName
	}
	return DefaultJobServiceAccountName
}

func jobTTLSeconds(policy *securityv1alpha1.AltImageUpdatePolicy) int32 {
	if policy.Spec.JobTemplate.TTLSecondsAfterFinished != nil {
		return *policy.Spec.JobTemplate.TTLSecondsAfterFinished
	}
	return DefaultJobTTLSeconds
}

func jobBackoffLimit(policy *securityv1alpha1.AltImageUpdatePolicy) int32 {
	if policy.Spec.JobTemplate.BackoffLimit != nil {
		return *policy.Spec.JobTemplate.BackoffLimit
	}
	return DefaultJobBackoffLimit
}

func jobActiveDeadlineSeconds(policy *securityv1alpha1.AltImageUpdatePolicy) int64 {
	if policy.Spec.JobTemplate.ActiveDeadlineSeconds != nil {
		return *policy.Spec.JobTemplate.ActiveDeadlineSeconds
	}
	return DefaultJobActiveDeadlineSeconds
}

func ownerReferences(policy *securityv1alpha1.AltImageUpdatePolicy) []metav1.OwnerReference {
	return []metav1.OwnerReference{
		{
			APIVersion:         securityv1alpha1.GroupVersion.String(),
			Kind:               "AltImageUpdatePolicy",
			Name:               policy.Name,
			UID:                policy.UID,
			Controller:         boolPtr(true),
			BlockOwnerDeletion: boolPtr(true),
		},
	}
}

func int32Ptr(value int32) *int32 {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}
