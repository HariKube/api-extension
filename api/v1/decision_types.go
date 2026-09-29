package v1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DecisionQuestion describes a single question for a decision request
type DecisionQuestion struct {
	Type         string                `json:"type" yaml:"type"`
	Instructions string                `json:"instructions" yaml:"instructions"`
	Criteria     *apiextensionsv1.JSON `json:"criteria,omitempty" yaml:"criteria,omitempty"`
}

// DecisionRequestSpec defines the desired state of DecisionRequest
type DecisionRequestSpec struct {
	// State contains arbitrary JSON/YAML representing the subject under review
	// +optional
	State *apiextensionsv1.JSON `json:"state,omitempty" yaml:"state,omitempty"`

	// Questions is a map of question-name to question definition
	// +required
	Questions map[string]DecisionQuestion `json:"questions" yaml:"questions"`
}

// +kubebuilder:object:root=true

// DecisionRequest is the Schema for the DecisionRequests API
type DecisionRequest struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DecisionRequest
	// +required
	Spec DecisionRequestSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// DecisionRequestList contains a list of DecisionRequest
type DecisionRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DecisionRequest `json:"items"`
}

// DecisionResponseSpec defines the observed state of DecisionResponse
type DecisionResponseSpec struct {
	// Answers contains arbitrary JSON/YAML representing the decision answers
	// +optional
	Answers *apiextensionsv1.JSON `json:"answers,omitempty" yaml:"answers,omitempty"`

	// Usage contains arbitrary JSON/YAML representing usage or metadata about the decision
	// +optional
	Usage *apiextensionsv1.JSON `json:"usage,omitempty" yaml:"usage,omitempty"`
}

// +kubebuilder:object:root=true

// DecisionResponse is the Schema for the DecisionResponses API
type DecisionResponse struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the observed state of DecisionResponse
	// +required
	Spec DecisionResponseSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// DecisionResponseList contains a list of DecisionResponse
type DecisionResponseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DecisionResponse `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DecisionRequest{}, &DecisionRequestList{}, &DecisionResponse{}, &DecisionResponseList{})
}
