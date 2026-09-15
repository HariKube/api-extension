package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// QuerySpec defines the desired state of Query
type QuerySpec struct {
	// Add fields here as needed in future
}

// +kubebuilder:object:root=true

// Query is the Schema for the Queries API
type Query struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Query
	// +required
	Spec QuerySpec `json:"spec"`
}

// +kubebuilder:object:root=true

// QueryList contains a list of Query
type QueryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Query `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Query{}, &QueryList{})
}
