/*
Copyright 2024 Keiailab.

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
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// MongoDBVersion defines MongoDB version configuration
type MongoDBVersion struct {
	// Version is the MongoDB version (e.g., "8.2")
	// +kubebuilder:validation:Pattern=`^\d+\.\d+(\.\d+)?$`
	Version string `json:"version"`

	// Image is the MongoDB container image
	// +optional
	Image string `json:"image,omitempty"`
}

// StorageSpec defines storage configuration
type StorageSpec struct {
	// StorageClassName is the name of the StorageClass
	// If not specified, the default storage class will be used
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// Size is the storage size
	// +kubebuilder:default="10Gi"
	Size resource.Quantity `json:"size,omitempty"`

	// DataDirPath is the path for MongoDB data
	// +kubebuilder:default="/data/db"
	DataDirPath string `json:"dataDirPath,omitempty"`
}

// ResourcesSpec defines resource requirements
type ResourcesSpec struct {
	// Requests describes minimum resources required
	// +optional
	Requests corev1.ResourceList `json:"requests,omitempty"`

	// Limits describes maximum resources allowed
	// +optional
	Limits corev1.ResourceList `json:"limits,omitempty"`
}

// TLSSpec defines TLS configuration
type TLSSpec struct {
	// Enabled enables TLS for MongoDB connections
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// CertManager enables cert-manager integration
	// +optional
	CertManager *CertManagerSpec `json:"certManager,omitempty"`

	// CustomCert references a custom TLS secret
	// +optional
	CustomCert *CustomCertSpec `json:"customCert,omitempty"`
}

// CertManagerSpec defines cert-manager configuration
type CertManagerSpec struct {
	// IssuerRef references a cert-manager Issuer or ClusterIssuer
	IssuerRef CertIssuerRef `json:"issuerRef"`

	// Duration is the certificate duration
	// +kubebuilder:default="2160h"
	Duration string `json:"duration,omitempty"`

	// RenewBefore is when to renew before expiry
	// +kubebuilder:default="360h"
	RenewBefore string `json:"renewBefore,omitempty"`
}

// CertIssuerRef references a cert-manager issuer
type CertIssuerRef struct {
	// Name is the issuer name
	Name string `json:"name"`

	// Kind is the issuer kind (Issuer or ClusterIssuer)
	// +kubebuilder:validation:Enum=Issuer;ClusterIssuer
	// +kubebuilder:default="ClusterIssuer"
	Kind string `json:"kind"`
}

// CustomCertSpec references custom certificates
type CustomCertSpec struct {
	// SecretName is the name of the TLS secret
	SecretName string `json:"secretName"`
}

// AuthSpec defines authentication configuration
type AuthSpec struct {
	// Mechanism defines the auth mechanism
	// +kubebuilder:validation:Enum=SCRAM-SHA-256;SCRAM-SHA-1;X509
	// +kubebuilder:default="SCRAM-SHA-256"
	Mechanism string `json:"mechanism,omitempty"`

	// AdminCredentialsSecretRef references the admin credentials secret
	AdminCredentialsSecretRef corev1.LocalObjectReference `json:"adminCredentialsSecretRef"`

	// Users defines additional users to create
	// +optional
	Users []MongoDBUser `json:"users,omitempty"`
}

// MongoDBUser defines a MongoDB user
type MongoDBUser struct {
	// Name is the username
	Name string `json:"name"`

	// DB is the authentication database
	DB string `json:"db"`

	// PasswordSecretRef references the password secret
	PasswordSecretRef corev1.SecretKeySelector `json:"passwordSecretRef"`

	// Roles defines user roles
	Roles []MongoDBRole `json:"roles"`
}

// MongoDBRole defines a MongoDB role
type MongoDBRole struct {
	// Name is the role name
	Name string `json:"name"`

	// DB is the database for the role
	DB string `json:"db"`
}

// MonitoringSpec defines Prometheus monitoring configuration
type MonitoringSpec struct {
	// Enabled enables Prometheus monitoring
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// ServiceMonitor enables ServiceMonitor creation
	// +optional
	ServiceMonitor *ServiceMonitorSpec `json:"serviceMonitor,omitempty"`

	// PrometheusRules enables PrometheusRule creation
	// +optional
	PrometheusRules *PrometheusRulesSpec `json:"prometheusRules,omitempty"`

	// Exporter configures the MongoDB exporter sidecar
	// +optional
	Exporter *ExporterSpec `json:"exporter,omitempty"`
}

// ServiceMonitorSpec defines ServiceMonitor configuration
type ServiceMonitorSpec struct {
	// Labels are additional labels for the ServiceMonitor
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Interval is the scrape interval
	// +kubebuilder:default="30s"
	Interval string `json:"interval,omitempty"`

	// Namespace is the namespace for the ServiceMonitor
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// PrometheusRulesSpec defines Prometheus alerting rules
type PrometheusRulesSpec struct {
	// Enabled enables default alerting rules
	Enabled bool `json:"enabled"`

	// Labels are additional labels for PrometheusRule
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// ExporterSpec defines MongoDB exporter configuration
type ExporterSpec struct {
	// Image is the exporter image
	// +kubebuilder:default="percona/mongodb_exporter:0.40"
	Image string `json:"image,omitempty"`

	// Resources defines exporter resource requirements
	// +optional
	Resources ResourcesSpec `json:"resources,omitempty"`
}

// BackupSpec defines backup configuration
type BackupSpec struct {
	// Enabled enables backup functionality
	Enabled bool `json:"enabled"`

	// Schedule is the cron schedule for automated backups
	// +optional
	Schedule string `json:"schedule,omitempty"`

	// Retention defines backup retention policy
	// +optional
	Retention *RetentionSpec `json:"retention,omitempty"`

	// Storage defines where to store backups
	Storage BackupStorageSpec `json:"storage"`

	// PITREnabled enables Point-in-Time Recovery
	// +kubebuilder:default=false
	PITREnabled bool `json:"pitrEnabled,omitempty"`

	// OplogRetentionHours defines oplog retention for PITR
	// +kubebuilder:default=24
	OplogRetentionHours int `json:"oplogRetentionHours,omitempty"`
}

// RetentionSpec defines backup retention policy
type RetentionSpec struct {
	// Days is the number of days to retain backups
	// +kubebuilder:default=7
	Days int `json:"days,omitempty"`

	// Count is the maximum number of backups to retain
	// +optional
	Count *int `json:"count,omitempty"`
}

// BackupStorageSpec defines backup storage location
type BackupStorageSpec struct {
	// Type is the storage type
	// +kubebuilder:validation:Enum=s3;pvc
	Type string `json:"type"`

	// S3 defines S3-compatible storage (including Ceph ObjectStore)
	// +optional
	S3 *S3StorageSpec `json:"s3,omitempty"`

	// PVC defines PVC-based storage
	// +optional
	PVC *PVCStorageSpec `json:"pvc,omitempty"`
}

// S3StorageSpec defines S3 storage configuration
type S3StorageSpec struct {
	// Bucket is the S3 bucket name
	Bucket string `json:"bucket"`

	// Endpoint is the S3 endpoint URL
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// Region is the S3 region
	// +optional
	Region string `json:"region,omitempty"`

	// CredentialsRef references the S3 credentials secret
	CredentialsRef corev1.LocalObjectReference `json:"credentialsRef"`

	// Prefix is the key prefix for backups
	// +optional
	Prefix string `json:"prefix,omitempty"`

	// InsecureSkipTLS skips TLS verification
	// +kubebuilder:default=false
	InsecureSkipTLS bool `json:"insecureSkipTLS,omitempty"`
}

// PVCStorageSpec defines PVC storage configuration
type PVCStorageSpec struct {
	// StorageClassName is the storage class for backup PVC
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// Size is the PVC size
	Size resource.Quantity `json:"size"`
}

// AutoScalingSpec defines auto-scaling configuration
type AutoScalingSpec struct {
	// Enabled enables auto-scaling
	Enabled bool `json:"enabled"`

	// MinReplicas is the minimum number of replicas
	// +kubebuilder:validation:Minimum=1
	MinReplicas int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the maximum number of replicas
	MaxReplicas int32 `json:"maxReplicas"`

	// Metrics defines scaling metrics
	// +optional
	Metrics []AutoScalingMetric `json:"metrics,omitempty"`
}

// AutoScalingMetric defines a scaling metric
type AutoScalingMetric struct {
	// Type is the metric type
	// +kubebuilder:validation:Enum=cpu;memory;custom
	Type string `json:"type"`

	// Target is the target value (percentage for cpu/memory, absolute for custom)
	Target int32 `json:"target"`

	// CustomMetric defines a custom Prometheus metric
	// +optional
	CustomMetric *CustomMetricSpec `json:"customMetric,omitempty"`
}

// CustomMetricSpec defines a custom Prometheus metric
type CustomMetricSpec struct {
	// Name is the metric name
	Name string `json:"name"`

	// Query is the Prometheus query (optional)
	// +optional
	Query string `json:"query,omitempty"`
}

// PodSpec defines pod-level configuration
type PodSpec struct {
	// SecurityContext defines pod security context
	// +optional
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`

	// ContainerSecurityContext defines container security context
	// +optional
	ContainerSecurityContext *corev1.SecurityContext `json:"containerSecurityContext,omitempty"`

	// Affinity defines pod affinity rules
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Tolerations defines pod tolerations
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// NodeSelector defines node selection constraints
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// PriorityClassName defines the priority class
	// +optional
	PriorityClassName string `json:"priorityClassName,omitempty"`

	// ServiceAccountName is the service account name
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// TopologySpreadConstraints describes how pods are spread across topology
	// +optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
}

// NetworkPolicySpec defines NetworkPolicy configuration for the workload pods.
// Enabled가 true면 controller가 NetworkPolicy를 생성한다 — 기본 정책은 같은
// RS/cluster의 pods간 ingress만 허용. AdditionalIngressRules는 사용자가 정의한
// 추가 ingress(예: 모니터링 namespace, 클라이언트 namespace)를 append.
//
// 기본값은 Enabled=false (opt-in) — 기존 운영 클러스터 트래픽을 의도치 않게
// 차단하지 않기 위함.
type NetworkPolicySpec struct {
	// Enabled enables NetworkPolicy creation
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// AdditionalIngressFrom appends extra peers (namespaces or pods) to the
	// generated ingress rule. 본 필드의 peers는 27017 포트로 ingress가 허용된다.
	// +optional
	AdditionalIngressFrom []NetworkPolicyPeer `json:"additionalIngressFrom,omitempty"`
}

// NetworkPolicyPeer는 NetworkPolicySpec의 추가 peer를 단순화한 타입이다.
// PodSelector + NamespaceSelector 조합 하나만 허용 (CIDR은 본 단계 비지원).
type NetworkPolicyPeer struct {
	// PodSelector selects pods within the chosen namespaces
	// +optional
	PodSelector *map[string]string `json:"podSelector,omitempty"`

	// NamespaceSelector selects namespaces by labels
	// +optional
	NamespaceSelector *map[string]string `json:"namespaceSelector,omitempty"`
}

// PodDisruptionBudgetSpec defines PodDisruptionBudget configuration for the workload pods.
// Enabled가 true면 controller가 PodDisruptionBudget을 생성한다.
// MinAvailable과 MaxUnavailable은 동시 지정 불가(K8s 제약). 둘 다 nil이면
// minAvailable = replicas-1을 기본값으로 적용한다(3 멤버 RS → minAvailable=2).
type PodDisruptionBudgetSpec struct {
	// Enabled enables PDB creation
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// MinAvailable is the minimum number/percentage of pods available during disruption
	// +optional
	MinAvailable *intstr.IntOrString `json:"minAvailable,omitempty"`

	// MaxUnavailable is the maximum number/percentage of pods unavailable during disruption
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
}

// ClusterReference references a MongoDB cluster
type ClusterReference struct {
	// Name is the cluster name
	Name string `json:"name"`

	// Kind is the cluster kind (MongoDB or MongoDBSharded)
	// +kubebuilder:validation:Enum=MongoDB;MongoDBSharded
	Kind string `json:"kind"`
}
