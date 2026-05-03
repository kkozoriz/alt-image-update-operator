/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AltBranch identifies an ALT Linux package repository branch.
type AltBranch string

const (
	// AltBranchP10 is the ALT Linux p10 branch.
	AltBranchP10 AltBranch = "p10"
	// AltBranchP11 is the ALT Linux p11 branch.
	AltBranchP11 AltBranch = "p11"
	// AltBranchSisyphus is the ALT Linux Sisyphus branch.
	AltBranchSisyphus AltBranch = "sisyphus"
)

// CheckMode controls how the operator decides whether to rebuild.
type CheckMode string

const (
	// CheckModeAlways always runs the build pipeline for a new policy run.
	CheckModeAlways CheckMode = "Always"
	// CheckModeAltAptSimulation checks for package updates with ALT apt simulation.
	CheckModeAltAptSimulation CheckMode = "AltAptSimulation"
)

// BuilderType selects the image builder implementation.
type BuilderType string

const (
	// BuilderTypeKaniko uses the Kaniko executor.
	BuilderTypeKaniko BuilderType = "Kaniko"
)

// BuildContextType selects the build context source.
type BuildContextType string

const (
	// BuildContextTypeConfigMap uses a ConfigMap as the build context.
	BuildContextTypeConfigMap BuildContextType = "ConfigMap"
)

// PolicyPhase describes the current reconcile state.
type PolicyPhase string

const (
	PolicyPhasePending    PolicyPhase = "Pending"
	PolicyPhaseChecking   PolicyPhase = "Checking"
	PolicyPhaseUpToDate   PolicyPhase = "UpToDate"
	PolicyPhaseBuilding   PolicyPhase = "Building"
	PolicyPhasePublishing PolicyPhase = "Publishing"
	PolicyPhaseApplying   PolicyPhase = "Applying"
	PolicyPhaseRollingOut PolicyPhase = "RollingOut"
	PolicyPhaseSucceeded  PolicyPhase = "Succeeded"
	PolicyPhaseFailed     PolicyPhase = "Failed"
)

const (
	ConditionReady             = "Ready"
	ConditionCheckCompleted    = "CheckCompleted"
	ConditionUpdatesAvailable  = "UpdatesAvailable"
	ConditionBuildCompleted    = "BuildCompleted"
	ConditionImagePublished    = "ImagePublished"
	ConditionDeploymentUpdated = "DeploymentUpdated"
	ConditionRolloutCompleted  = "RolloutCompleted"
	ConditionUpToDate          = "UpToDate"
	ConditionFailed            = "Failed"
)

// AltImageUpdatePolicySpec defines the desired state of AltImageUpdatePolicy.
type AltImageUpdatePolicySpec struct {
	// Alt describes the ALT Linux base image and package branch used by the policy.
	// +kubebuilder:validation:Required
	Alt AltSpec `json:"alt"`

	// Trigger contains user-controlled inputs that start a new policy run.
	// +optional
	Trigger TriggerSpec `json:"trigger,omitempty"`

	// Check controls whether the policy always rebuilds or first checks ALT package updates.
	// +kubebuilder:validation:Required
	Check CheckSpec `json:"check"`

	// Build describes the image build and push configuration.
	// +kubebuilder:validation:Required
	Build BuildSpec `json:"build"`

	// TargetRef identifies the Deployment updated by this policy. It must be in the same namespace as the policy.
	// +kubebuilder:validation:Required
	TargetRef TargetReference `json:"targetRef"`

	// ContainerName is the Deployment container whose image should be updated.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ContainerName string `json:"containerName"`

	// Rollout controls Deployment rollout observation.
	// +optional
	Rollout RolloutSpec `json:"rollout,omitempty"`

	// JobTemplate contains common settings for check and build Jobs created by the controller.
	// +optional
	JobTemplate JobTemplateSpec `json:"jobTemplate,omitempty"`
}

// AltSpec defines ALT Linux inputs for check and build jobs.
type AltSpec struct {
	// Branch is the ALT Linux package branch.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=p10;p11;sisyphus
	Branch AltBranch `json:"branch"`

	// BaseImage is the ALT Linux image used by check jobs and exposed to build jobs.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^\S+$`
	BaseImage string `json:"baseImage"`
}

// TriggerSpec defines manual retrigger settings.
type TriggerSpec struct {
	// ManualToken starts a new policy run when changed.
	// +optional
	ManualToken string `json:"manualToken,omitempty"`
}

// CheckSpec defines how the controller checks for available updates.
type CheckSpec struct {
	// Mode is the check strategy.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Always;AltAptSimulation
	Mode CheckMode `json:"mode"`
}

// BuildSpec defines the image build and registry push settings.
type BuildSpec struct {
	// Builder selects the build backend. MVP supports only Kaniko.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Kaniko
	Builder BuilderType `json:"builder"`

	// BuilderImage overrides the Kaniko executor image.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^\S+$`
	BuilderImage string `json:"builderImage,omitempty"`

	// OutputImage is the destination image repository, with an optional tag replaced by the controller build tag.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^\S+$`
	OutputImage string `json:"outputImage"`

	// TagTemplate optionally customizes the generated image tag.
	// +optional
	TagTemplate string `json:"tagTemplate,omitempty"`

	// Context describes where the build context comes from.
	// +kubebuilder:validation:Required
	Context BuildContextSpec `json:"context"`

	// RegistrySecretRef points to a Secret containing Docker registry credentials.
	// +optional
	RegistrySecretRef *corev1.LocalObjectReference `json:"registrySecretRef,omitempty"`
}

// BuildContextSpec defines the source of a build context.
type BuildContextSpec struct {
	// Type selects the build context source. MVP supports only ConfigMap.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=ConfigMap
	Type BuildContextType `json:"type"`

	// ConfigMapRef references the ConfigMap containing the build context.
	// +kubebuilder:validation:Required
	ConfigMapRef ConfigMapBuildContextRef `json:"configMapRef"`
}

// ConfigMapBuildContextRef identifies a ConfigMap build context and Dockerfile key.
type ConfigMapBuildContextRef struct {
	// Name is the ConfigMap name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// DockerfileKey is the ConfigMap data key containing the Dockerfile.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DockerfileKey string `json:"dockerfileKey"`
}

// TargetReference identifies the workload updated by this policy.
type TargetReference struct {
	// APIVersion is the API version of the target. MVP supports only apps/v1.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=apps/v1
	APIVersion string `json:"apiVersion"`

	// Kind is the target kind. MVP supports only Deployment.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Deployment
	Kind string `json:"kind"`

	// Name is the target Deployment name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// RolloutSpec defines rollout wait settings.
type RolloutSpec struct {
	// TimeoutSeconds is the maximum time to wait for Deployment rollout completion.
	// +optional
	// +kubebuilder:validation:Minimum=1
	TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`
}

// JobTemplateSpec defines common settings for Jobs created by the controller.
type JobTemplateSpec struct {
	// ServiceAccountName is assigned to check and build Pods.
	// +optional
	// +kubebuilder:validation:MinLength=1
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// HostNetwork runs check and build Pods on the node network.
	// This is useful for demo clusters that expose a node-local registry such as localhost:5000.
	// +optional
	HostNetwork bool `json:"hostNetwork,omitempty"`

	// TTLSecondsAfterFinished sets the cleanup TTL for completed Jobs.
	// +optional
	// +kubebuilder:validation:Minimum=0
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// BackoffLimit controls Job retry count.
	// +optional
	// +kubebuilder:validation:Minimum=0
	BackoffLimit *int32 `json:"backoffLimit,omitempty"`

	// ActiveDeadlineSeconds limits Job runtime.
	// +optional
	// +kubebuilder:validation:Minimum=1
	ActiveDeadlineSeconds *int64 `json:"activeDeadlineSeconds,omitempty"`
}

// AltImageUpdatePolicyStatus defines the observed state of AltImageUpdatePolicy.
type AltImageUpdatePolicyStatus struct {
	// ObservedGeneration is the latest metadata.generation processed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Phase is the current policy state.
	// +optional
	// +kubebuilder:validation:Enum=Pending;Checking;UpToDate;Building;Publishing;Applying;RollingOut;Succeeded;Failed
	Phase PolicyPhase `json:"phase,omitempty"`

	// BuildID is the stable identifier for the current policy run.
	// +optional
	BuildID string `json:"buildID,omitempty"`

	// CurrentRunKey is the stable run key derived from generation and trigger inputs.
	// +optional
	CurrentRunKey string `json:"currentRunKey,omitempty"`

	// LastCheckTime is the last time the check stage completed or was processed.
	// +optional
	LastCheckTime *metav1.Time `json:"lastCheckTime,omitempty"`

	// LastBuildStartTime is the last time the build stage started.
	// +optional
	LastBuildStartTime *metav1.Time `json:"lastBuildStartTime,omitempty"`

	// LastBuildCompletionTime is the last time the build stage completed.
	// +optional
	LastBuildCompletionTime *metav1.Time `json:"lastBuildCompletionTime,omitempty"`

	// LastApplyTime is the last time the target Deployment was patched.
	// +optional
	LastApplyTime *metav1.Time `json:"lastApplyTime,omitempty"`

	// LastRolloutTime is the last time the target Deployment rollout completed.
	// +optional
	LastRolloutTime *metav1.Time `json:"lastRolloutTime,omitempty"`

	// LastCheckJobName is the most recent check Job name.
	// +optional
	LastCheckJobName string `json:"lastCheckJobName,omitempty"`

	// LastBuildJobName is the most recent build Job name.
	// +optional
	LastBuildJobName string `json:"lastBuildJobName,omitempty"`

	// LastBuiltImage is the most recent image produced by a build Job.
	// +optional
	LastBuiltImage string `json:"lastBuiltImage,omitempty"`

	// LastAppliedImage is the most recent image applied to the target Deployment.
	// +optional
	LastAppliedImage string `json:"lastAppliedImage,omitempty"`

	// TargetDeploymentGeneration is the target Deployment generation after patching.
	// +optional
	TargetDeploymentGeneration int64 `json:"targetDeploymentGeneration,omitempty"`

	// Reason is a machine-readable reason for the current phase.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message is a human-readable description of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// Conditions contain detailed Kubernetes-style condition state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetRef.name`
// +kubebuilder:printcolumn:name="BuiltImage",type=string,JSONPath=`.status.lastBuiltImage`
// +kubebuilder:printcolumn:name="AppliedImage",type=string,JSONPath=`.status.lastAppliedImage`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AltImageUpdatePolicy is the Schema for the altimageupdatepolicies API.
type AltImageUpdatePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AltImageUpdatePolicySpec   `json:"spec"`
	Status AltImageUpdatePolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AltImageUpdatePolicyList contains a list of AltImageUpdatePolicy.
type AltImageUpdatePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AltImageUpdatePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AltImageUpdatePolicy{}, &AltImageUpdatePolicyList{})
}
