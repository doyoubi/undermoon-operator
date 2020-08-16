/*


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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// UndermoonSpec defines the desired state of Undermoon
type UndermoonSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:MaxLength=30
	// +kubebuilder:validation:MinLength=1
	ClusterName string `json:"clusterName"`
	// Each chunk has 2 masters and 2 replicas. This field is used to specify node number of the cluster.
	// +kubebuilder:validation:Minimum=1
	ChunkNumber uint32 `json:"chunkNumber"`
	// max_memory for each Redis instance in MBs.
	// +kubebuilder:validation:Minimum=1
	MaxMemory uint32 `json:"maxMemory"`
	// Port for the redis service.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port uint32 `json:"port"`
	// Enable this to let the shards redirect the requests themselves so that the client does not need to support cluster mode.
	ActiveRedirection bool `json:"activeRedirection"`
	// +kubebuilder:validation:Minimum=1
	ProxyThreads uint32 `json:"proxyThreads"`

	// +kubebuilder:validation:MinLength=1
	UndermoonImage           string            `json:"undermoonImage"`
	UndermoonImagePullPolicy corev1.PullPolicy `json:"undermoonImagePullPolicy"`
	// +kubebuilder:validation:MinLength=1
	RedisImage string `json:"redisImage"`

	// +optional
	BrokerResources corev1.ResourceRequirements `json:"brokerResources"`
	// +optional
	CoordinatorResources corev1.ResourceRequirements `json:"coordinatorResources"`
	// +optional
	ProxyResources corev1.ResourceRequirements `json:"proxyResources"`
	// +optional
	RedisResources corev1.ResourceRequirements `json:"redisResources"`
}

// UndermoonStatus defines the observed state of Undermoon
type UndermoonStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Master broker address pointing to the master broker.
	// +kubebuilder:validation:MinLength=1
	MasterBrokerAddress string `json:"masterBrokerAddress"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Undermoon is the Schema for the undermoons API
type Undermoon struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UndermoonSpec   `json:"spec,omitempty"`
	Status UndermoonStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// UndermoonList contains a list of Undermoon
type UndermoonList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Undermoon `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Undermoon{}, &UndermoonList{})
}
