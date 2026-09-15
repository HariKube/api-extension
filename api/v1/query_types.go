package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// QuerySpec defines the desired state of Query
type QuerySpec struct {
	// Add fields here as needed in future
}

// Query is the Schema for the Queries API
type Query struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of Query
	// +required
	Spec QuerySpec `json:"spec"`
}

// QueryList contains a list of Query
type QueryList struct {
	metav1.TypeMeta `json:",inline"`

	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Query `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Query{}, &QueryList{})
}
