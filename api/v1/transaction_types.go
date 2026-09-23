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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// TransactionRequestSpec defines the desired state of Transaction
type TransactionRequestSpec struct {
	Create [][]byte `json:"create" yaml:"create"`
	Update [][]byte `json:"update" yaml:"update"`
	Delete [][]byte `json:"delete" yaml:"delete"`
}

// +kubebuilder:object:root=true

// TransactionRequest is the Schema for the Transactions API
type TransactionRequest struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Transaction
	// +required
	Spec TransactionRequestSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// TransactionRequestList contains a list of Transaction
type TransactionRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TransactionRequest `json:"items"`
}

// TransactionResponseSpec defines the actual state of Transaction
type TransactionResponseSpec struct {
	Error string `json:"error"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Error",type="string",JSONPath=".spec.error"

// TransactionResponse is the Schema for the Transactions API
type TransactionResponse struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the actual state of Transaction
	// +required
	Spec TransactionResponseSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// TransactionResponseList contains a list of TransactionResponse
type TransactionResponseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TransactionResponse `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TransactionRequest{}, &TransactionRequestList{}, &TransactionResponse{}, &TransactionResponseList{})
}
