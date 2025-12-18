/*
Copyright 2025 ContainerInfra.

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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition types for DeploymentCoordination
const (
	// ConditionTypeReady indicates that all coordinated deployments have finished rolling out.
	ConditionTypeReady = "Ready"
	// ConditionTypeProgressing indicates that a deployment is currently rolling out.
	ConditionTypeProgressing = "Progressing"
	// ConditionTypeDegraded indicates that there is an error or issue with the coordination.
	ConditionTypeDegraded = "Degraded"
)

// DeploymentCoordinationSpec defines the desired state of DeploymentCoordination.
type DeploymentCoordinationSpec struct {
	// MinReadySeconds is the minimum number of seconds that the last active deployment must be ready before continuing with the next deployment.
	MinReadySeconds int32 `json:"minReadySeconds,omitempty"`
	// LabelSelector is the label selector for the deployments to coordinate.
	LabelSelector metav1.LabelSelector `json:"labelSelector,omitempty"`
}

// DeploymentState represents the state of a coordinated deployment.
type DeploymentState struct {
	// Name is the deployment key (namespace/name).
	Name string `json:"name"`
	// HasPendingChanges indicates if the deployment needs rollout based on replica counts or new spec.
	HasPendingChanges bool `json:"hasPendingChanges,omitempty"`
	// Generation is the current generation of the deployment (for reference).
	Generation int64 `json:"generation,omitempty"`
	// LastRolloutStarted is the timestamp when the last rollout started.
	// This is set when a deployment becomes active and starts rolling out.
	// This timestamp is preserved even after the rollout finishes for historical tracking.
	// +optional
	LastRolloutStarted *metav1.Time `json:"lastRolloutStarted,omitempty"`
	// LastRolloutFinished is the timestamp when the last rollout finished.
	// This is set when a deployment completes rolling out.
	// This timestamp is preserved for historical tracking.
	// +optional
	LastRolloutFinished *metav1.Time `json:"lastRolloutFinished,omitempty"`
}

// DeploymentCoordinationStatus defines the observed state of DeploymentCoordination.
type DeploymentCoordinationStatus struct {
	ActiveDeployment string `json:"activeDeployment,omitempty"`
	// Deployments is the list of deployments that are coordinated based on the label selector.
	Deployments []string `json:"deployments,omitempty"`
	// DeploymentStates tracks the state of each coordinated deployment.
	// +listType=map
	// +listMapKey=name
	DeploymentStates []DeploymentState `json:"deploymentStates,omitempty"`
	// Conditions represent the latest available observations of the DeploymentCoordination's state.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Active",type=string,JSONPath=`.status.activeDeployment`,description="Active deployment"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`,description="Ready condition status"
// +kubebuilder:printcolumn:name="Progressing",type=string,JSONPath=`.status.conditions[?(@.type=="Progressing")].status`,description="Progressing condition status"
// +kubebuilder:subresource:status

// DeploymentCoordination is the Schema for the deploymentcoordinations API.
type DeploymentCoordination struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DeploymentCoordinationSpec   `json:"spec,omitempty"`
	Status DeploymentCoordinationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DeploymentCoordinationList contains a list of DeploymentCoordination.
type DeploymentCoordinationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DeploymentCoordination `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DeploymentCoordination{}, &DeploymentCoordinationList{})
}
