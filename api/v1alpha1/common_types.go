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
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	commonsversion "github.com/keiailab/operator-commons/pkg/version"
)

// MongoDBVersion defines MongoDB version configuration
type MongoDBVersion struct {
	// Version is the MongoDB version (e.g., "8.3.1")
	// +kubebuilder:validation:Pattern=`^\d+\.\d+(\.\d+)?$`
	Version string `json:"version"`

	// Image is the MongoDB container image
	// +optional
	Image string `json:"image,omitempty"`
}

// supportedMongoDBList — major.minor 화이트리스트. patch 버전은 semver-prefix 매칭.
// 사용자 요구 (iteration 9, 2026-05-07): 최소 마일스톤 2개 버전 호환 — N + N-1 + N-2.
// 8.0 = LTS baseline, 8.2 = stable, 8.3 = current. 신규 추가 시 oplog format,
// replica set wire protocol, sharding chunk format 호환성 검증 후 추가.
var supportedMongoDBList = commonsversion.MustList("8.0", "8.2", "8.3")

// SupportedMongoDBVersions — 외부 노출 슬라이스 (chart values / docs / 기존 호환).
// major.minor 형식. 사용자가 8.3.1 같이 patch 까지 명시해도 IsSupportedMongoDBVersion
// 가 prefix 매칭으로 허용.
var SupportedMongoDBVersions = supportedMongoDBList.Strings()

// IsSupportedMongoDBVersion — webhook validation / controller 검증 헬퍼.
// patch 레벨까지 받은 v 에서 major.minor 추출 후 화이트리스트 비교.
//
// 예:
//
//	IsSupportedMongoDBVersion("8.3.1") == true   (8.3 매칭)
//	IsSupportedMongoDBVersion("8.3")   == true
//	IsSupportedMongoDBVersion("8.0.0") == true
//	IsSupportedMongoDBVersion("7.0.5") == false
//	IsSupportedMongoDBVersion("9.0")   == false
//	IsSupportedMongoDBVersion("8")     == false  (major-only 거부)
//	IsSupportedMongoDBVersion("")      == false
func IsSupportedMongoDBVersion(v string) bool {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	majorMinor := parts[0] + "." + parts[1]
	return supportedMongoDBList.IsSupported(majorMinor)
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

	// PersistentVolumeClaimRetentionPolicy describes the lifecycle of PVCs
	// created from the StatefulSet's VolumeClaimTemplates. WhenDeleted/WhenScaled
	// 모두 Retain(기본) 또는 Delete를 허용. 기본은 Retain — 운영자가 명시적으로
	// 데이터 폐기를 선택하기 전까지 PVC를 보존하기 위함.
	// +optional
	PersistentVolumeClaimRetentionPolicy *appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy `json:"persistentVolumeClaimRetentionPolicy,omitempty"`

	// Encryption defines optional encryption-at-rest configuration (F-IMP-02 / F38 cycle 6).
	// MongoDB Enterprise WiredTiger 의 encryption-at-rest 정합 — 본 cycle 의
	// acceptance 는 *키 provider config 정합 + Secret/Vault/Cloud KMS 매핑*
	// 까지. 실 키 회전 자동화는 cycle 9+ 운영 강화 시점.
	// +optional
	Encryption *EncryptionSpec `json:"encryption,omitempty"`
}

// EncryptionSpec — F38 (cycle 6). Storage encryption-at-rest 설정.
type EncryptionSpec struct {
	// Enabled toggles WiredTiger encryption-at-rest.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// KeyProvider = secret | vault | aws-kms | gcp-kms | azure-kv
	// +kubebuilder:validation:Enum=secret;vault;aws-kms;gcp-kms;azure-kv
	// +kubebuilder:default="secret"
	KeyProvider string `json:"keyProvider,omitempty"`

	// KMSConfig holds provider-specific configuration. 각 provider 가 자체
	// sub-spec 을 채움 (mutually exclusive — provider 별 1건만).
	// +optional
	KMSConfig *KMSConfigSpec `json:"kmsConfig,omitempty"`

	// CipherMode = AES256-CBC | AES256-GCM (MongoDB 6.0+ GCM).
	// +kubebuilder:validation:Enum=AES256-CBC;AES256-GCM
	// +kubebuilder:default="AES256-GCM"
	CipherMode string `json:"cipherMode,omitempty"`

	// KeyRotationDays — 0 = no automatic rotation. >0 = controller가 N 일마다
	// 새 키 발급 + 기존 키 archive.
	// +kubebuilder:default=0
	KeyRotationDays int32 `json:"keyRotationDays,omitempty"`
}

// KMSConfigSpec — provider 별 sub-config.
type KMSConfigSpec struct {
	// Secret (KeyProvider=secret): keyfile Secret 직접 참조. 단일 마스터키.
	// +optional
	Secret *SecretKMSConfig `json:"secret,omitempty"`

	// Vault (KeyProvider=vault): HashiCorp Vault Transit engine.
	// +optional
	Vault *VaultKMSConfig `json:"vault,omitempty"`

	// AWSKMS (KeyProvider=aws-kms): AWS KMS CMK ARN.
	// +optional
	AWSKMS *AWSKMSConfig `json:"awsKMS,omitempty"`

	// GCPKMS (KeyProvider=gcp-kms): GCP KMS keyring + key.
	// +optional
	GCPKMS *GCPKMSConfig `json:"gcpKMS,omitempty"`

	// AzureKV (KeyProvider=azure-kv): Azure Key Vault key URL.
	// +optional
	AzureKV *AzureKVConfig `json:"azureKV,omitempty"`
}

// SecretKMSConfig — single master keyfile in K8s Secret.
type SecretKMSConfig struct {
	// SecretRef.Name + Key = base64 32-byte raw key.
	SecretRef corev1.SecretKeySelector `json:"secretRef"`
}

// VaultKMSConfig — Vault Transit engine.
type VaultKMSConfig struct {
	// Address e.g. "https://vault.example.com:8200"
	// +kubebuilder:validation:MinLength=1
	Address string `json:"address"`

	// TransitPath default `transit`.
	// +kubebuilder:default="transit"
	TransitPath string `json:"transitPath,omitempty"`

	// KeyName Vault key identifier (Transit engine 의 named key).
	// +kubebuilder:validation:MinLength=1
	KeyName string `json:"keyName"`

	// AuthMethod = token | kubernetes | approle
	// +kubebuilder:validation:Enum=token;kubernetes;approle
	// +kubebuilder:default="kubernetes"
	AuthMethod string `json:"authMethod,omitempty"`

	// AuthSecretRef references K8s Secret with auth credentials
	// (token / role + jwt / role-id + secret-id).
	AuthSecretRef corev1.LocalObjectReference `json:"authSecretRef"`

	// CASecretRef optional TLS CA for Vault server verification.
	// +optional
	CASecretRef *corev1.LocalObjectReference `json:"caSecretRef,omitempty"`
}

// AWSKMSConfig — AWS KMS CMK reference.
type AWSKMSConfig struct {
	// KeyARN e.g. "arn:aws:kms:us-east-1:123:key/abc-..."
	// +kubebuilder:validation:Pattern=`^arn:aws:kms:.+:.+:key/.+$`
	KeyARN string `json:"keyARN"`

	// Region e.g. "us-east-1"
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`

	// AuthSecretRef IRSA-aware. Secret keys: access_key_id / secret_access_key.
	// IRSA 사용 시 빈 secret + ServiceAccount annotations 로 인증.
	// +optional
	AuthSecretRef *corev1.LocalObjectReference `json:"authSecretRef,omitempty"`
}

// GCPKMSConfig — Google Cloud KMS reference.
type GCPKMSConfig struct {
	// ProjectID GCP project containing the keyring.
	// +kubebuilder:validation:MinLength=1
	ProjectID string `json:"projectID"`

	// Location e.g. "us-central1" or "global".
	// +kubebuilder:validation:MinLength=1
	Location string `json:"location"`

	// Keyring + Key 식별자.
	// +kubebuilder:validation:MinLength=1
	Keyring string `json:"keyring"`
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	// AuthSecretRef Workload Identity 시 빈 secret. 그 외에 SA JSON Secret.
	// +optional
	AuthSecretRef *corev1.LocalObjectReference `json:"authSecretRef,omitempty"`
}

// AzureKVConfig — Azure Key Vault reference.
type AzureKVConfig struct {
	// VaultURL e.g. "https://my-kv.vault.azure.net"
	// +kubebuilder:validation:Pattern=`^https://.+\.vault\.azure\.net/?$`
	VaultURL string `json:"vaultURL"`

	// KeyName + KeyVersion (optional, latest if empty).
	// +kubebuilder:validation:MinLength=1
	KeyName string `json:"keyName"`
	// +optional
	KeyVersion string `json:"keyVersion,omitempty"`

	// TenantID + ClientID — workload identity 시 ServiceAccount 로 매핑.
	// +optional
	TenantID string `json:"tenantID,omitempty"`
	// +optional
	ClientID string `json:"clientID,omitempty"`

	// AuthSecretRef SP credentials (client_secret) — workload identity 사용 시 nil.
	// +optional
	AuthSecretRef *corev1.LocalObjectReference `json:"authSecretRef,omitempty"`
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
	// Mechanism defines the auth mechanism. PLAIN 은 LDAP-backed
	// (sasl-passthrough), MONGODB-OIDC 는 OIDC IdP bind.
	// +kubebuilder:validation:Enum=SCRAM-SHA-256;SCRAM-SHA-1;X509;PLAIN;MONGODB-OIDC
	// +kubebuilder:default="SCRAM-SHA-256"
	Mechanism string `json:"mechanism,omitempty"`

	// AdminCredentialsSecretRef references the admin credentials secret
	AdminCredentialsSecretRef corev1.LocalObjectReference `json:"adminCredentialsSecretRef"`

	// Users defines additional users to create
	// +optional
	Users []MongoDBUser `json:"users,omitempty"`

	// LDAP defines optional LDAP-backed enterprise authentication.
	// Mechanism 이 PLAIN 또는 SCRAM-SHA-256 일 때 sasl-passthrough 적용.
	// +optional
	LDAP *LDAPSpec `json:"ldap,omitempty"`

	// OIDC defines optional OIDC / OAuth2 authentication.
	// Mechanism 이 MONGODB-OIDC 일 때 활성. Keycloak / Okta / Auth0
	// 호환 — issuerURL + clientID 매핑.
	// +optional
	OIDC *OIDCSpec `json:"oidc,omitempty"`
}

// LDAPSpec — F23 (cycle 4). MongoDB Enterprise 의 LDAP authentication +
// authorization 정합. 본 spec 는 *OSS 호환 sasl-passthrough* 모델.
type LDAPSpec struct {
	// Servers is a comma-separated list of LDAP server hostnames
	// (host:port). MongoDB 가 client 로 접속할 LDAP server.
	// +kubebuilder:validation:MinLength=1
	Servers string `json:"servers"`

	// BindMethod = simple | sasl. simple 은 bind DN + password, sasl 은
	// SASL mechanism (e.g. PLAIN).
	// +kubebuilder:validation:Enum=simple;sasl
	// +kubebuilder:default="simple"
	BindMethod string `json:"bindMethod,omitempty"`

	// BindCredentialsSecretRef references LDAP service account credentials
	// (keys: bindDN / password) for user lookup.
	// +optional
	BindCredentialsSecretRef *corev1.LocalObjectReference `json:"bindCredentialsSecretRef,omitempty"`

	// UserToDNMapping is a MongoDB-format JSON array string for LDAP user
	// to DN transformation. e.g. `[{match:"(.+)",ldapQuery:"OU=Users,DC=ex,DC=com??sub?(uid={0})"}]`
	// +optional
	UserToDNMapping string `json:"userToDNMapping,omitempty"`

	// TLS enables LDAP over TLS (ldaps://). 별도 CA secret 필요 시
	// CASecretRef 사용.
	// +kubebuilder:default=true
	TLS bool `json:"tls,omitempty"`

	// CASecretRef references the LDAP CA bundle (key: ca.crt).
	// +optional
	CASecretRef *corev1.LocalObjectReference `json:"caSecretRef,omitempty"`

	// AuthorizationQueryTemplate is the LDAP query template for role
	// mapping. e.g. `{USER}?memberOf?base`.
	// +optional
	AuthorizationQueryTemplate string `json:"authorizationQueryTemplate,omitempty"`
}

// OIDCSpec — F28 (cycle 4). MongoDB 7.0+ OIDC authentication.
// Keycloak / Okta / Auth0 / Google IdP 호환.
type OIDCSpec struct {
	// IssuerURL is the OIDC provider issuer (must match `iss` claim).
	// +kubebuilder:validation:MinLength=1
	IssuerURL string `json:"issuerURL"`

	// ClientID is the OIDC client identifier registered with the IdP.
	// +kubebuilder:validation:MinLength=1
	ClientID string `json:"clientID"`

	// UserClaim is the JWT claim used as MongoDB username. Default `sub`.
	// +kubebuilder:default="sub"
	UserClaim string `json:"userClaim,omitempty"`

	// RolesClaim is the JWT claim that carries role memberships.
	// Default `groups`.
	// +kubebuilder:default="groups"
	RolesClaim string `json:"rolesClaim,omitempty"`

	// SupportedIdentityProviders 정합 hint — IdP 호환 검증용.
	// +kubebuilder:validation:Enum=keycloak;okta;auth0;google;generic
	// +kubebuilder:default="generic"
	IdentityProvider string `json:"identityProvider,omitempty"`
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
// MonitoringSpec defines monitoring configuration.
//
// Deprecated: spec 정의되어 있으나 *현 controller 미구현* — 사용자가 본 영역
// 설정해도 silent ignore (UX 함정). ADR-0018 Phase 1 단계.
//
// 단계적 해소:
//   - Phase 1 (즉시): godoc deprecation marker (본 코드).
//   - Phase 2 (C25 Prometheus 도입 후): 사용 빈도 측정 후 옵션 결정.
//   - Phase 3 (조건부): v2alpha1 삭제 또는 valkey 패턴 controller 구현.
//
// 임시 우회: chart-level `templates/servicemonitor.yaml` 정적 manifest 가
// operator pod 의 metrics endpoint scrape 만 cover (CR 인스턴스 metrics 는
// 별도 외부 ServiceMonitor 작성 필요).
type MonitoringSpec struct {
	// Enabled enables Prometheus monitoring
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// ServiceMonitor enables ServiceMonitor creation
	//
	// Deprecated: ADR-0018 Phase 1 — controller 미구현. 설정해도 ServiceMonitor
	// 자동 생성 안 됨. valkey-operator `internal/resources/servicemonitor.go`
	// 패턴 (commons.monitoring 위임 + fail-soft applyServiceMonitor) 차용은
	// Phase 2 결정 영역.
	// +optional
	ServiceMonitor *ServiceMonitorSpec `json:"serviceMonitor,omitempty"`

	// PrometheusRules enables PrometheusRule creation
	//
	// Deprecated: ADR-0018 Phase 1 — controller 미구현. PrometheusRule CRD 도
	// 클러스터에 미설치 가능 (C25 cluster-ops audit).
	// +optional
	PrometheusRules *PrometheusRulesSpec `json:"prometheusRules,omitempty"`

	// Exporter configures the MongoDB exporter sidecar
	//
	// Deprecated: ADR-0018 Phase 1 — controller 미구현. argos 운영의 mongodb
	// exporter 는 chart values `mongodb.exporterImage` + sharded mongos pod 의
	// sidecar 정적 정의로 cover.
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

// AuditLogSpec — F61-F65 (cycle 8) MongoDB Enterprise audit log 정합.
type AuditLogSpec struct {
	// Enabled toggles mongod auditLog 모듈.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// Destination = file | syslog | console
	// +kubebuilder:validation:Enum=file;syslog;console
	// +kubebuilder:default="file"
	Destination string `json:"destination,omitempty"`

	// Format = JSON | BSON
	// +kubebuilder:validation:Enum=JSON;BSON
	// +kubebuilder:default="JSON"
	Format string `json:"format,omitempty"`

	// FilterJSON — MongoDB audit filter expression. 미설정 시 모든 이벤트.
	// e.g. `{ "atype": { "$in": ["authenticate", "createUser", "dropUser"] } }`
	// +optional
	FilterJSON string `json:"filterJSON,omitempty"`

	// CentralForwarder — Loki / Elasticsearch / OpenSearch 통합 destination.
	// 설정 시 mongod 옆에 fluent-bit forwarder sidecar 가 추가.
	// +optional
	CentralForwarder *AuditForwarderSpec `json:"centralForwarder,omitempty"`

	// AlertRules — audit event 기반 PrometheusRule.
	// +optional
	AlertRules []AuditAlertRule `json:"alertRules,omitempty"`
}

// AuditForwarderSpec — fluent-bit forwarder sidecar 설정.
type AuditForwarderSpec struct {
	// Type = loki | elasticsearch | opensearch
	// +kubebuilder:validation:Enum=loki;elasticsearch;opensearch
	Type string `json:"type"`

	// URL forwarder endpoint.
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// CredentialsSecretRef optional (Loki/Elasticsearch 인증 필요 시).
	// +optional
	CredentialsSecretRef string `json:"credentialsSecretRef,omitempty"`
}

// AuditAlertRule — audit event 기반 알람.
type AuditAlertRule struct {
	// Name PrometheusRule 의 alert name.
	Name string `json:"name"`

	// EventType = authenticate | createUser | dropUser | replSetReconfig | shutdown | ...
	// +kubebuilder:validation:MinLength=1
	EventType string `json:"eventType"`

	// Threshold — 5-minute window 안에서 발생 횟수 임계.
	// +kubebuilder:default=10
	Threshold int32 `json:"threshold,omitempty"`

	// Severity = info | warning | critical
	// +kubebuilder:validation:Enum=info;warning;critical
	// +kubebuilder:default="warning"
	Severity string `json:"severity,omitempty"`
}

// UpgradeStrategySpec — F12-F15 (cycle 7) 자동 버전 업그레이드 정책.
type UpgradeStrategySpec struct {
	// Type = RollingUpdate (기본) | Manual.
	// +kubebuilder:validation:Enum=RollingUpdate;Manual
	// +kubebuilder:default="RollingUpdate"
	Type string `json:"type,omitempty"`

	// PreUpgradeBackup 가 true 이면 controller 가 upgrade 전 MongoDBBackup
	// CR 을 자동 생성하여 *현재 시점 snapshot* 확보. backup PASS 전까지
	// upgrade 진입 차단.
	// +kubebuilder:default=false
	PreUpgradeBackup bool `json:"preUpgradeBackup,omitempty"`

	// ValidationInterval — pod 한 개 upgrade 후 다음 pod 진입까지 대기 시간.
	// e.g. "60s" → 1분 검증 후 다음 pod. 짧으면 RPO 작지만 회귀 늦게 감지.
	// +kubebuilder:default="60s"
	ValidationInterval string `json:"validationInterval,omitempty"`

	// RollbackOnFailure 가 true 이면 검증 실패 시 controller 가 자동 롤백.
	// false 시 사용자 수동 개입 필요.
	// +kubebuilder:default=false
	RollbackOnFailure bool `json:"rollbackOnFailure,omitempty"`
}

// IsValidUpgradePath — F11 (cycle 7) 버전 업그레이드 path 검증.
// 본 함수는 webhook ValidateUpdate 에서 호출되어 *위험한 업그레이드 path*
// 를 차단한다.
//
// 정합 규칙:
//   - 동일 버전 (no-op): 허용
//   - patch 변경 (8.0.0 → 8.0.5): 허용
//   - 단일 minor +1 (8.0 → 8.2): 허용  (홀수 minor 는 dev 라 skip 가능)
//   - minor skip (8.0 → 8.3): MongoDB 가 단일 step upgrade 요구 → 차단
//   - major skip (8.x → 9.x): 차단
//   - downgrade (8.2 → 8.0): featureCompatibilityVersion 호환 미보장 → 차단
//   - unknown version (예: 7.0): IsSupportedMongoDBVersion 으로 사전 차단
//
// 반환: nil = 허용 / non-nil = reject 사유.
func IsValidUpgradePath(from, to string) error {
	if from == to {
		return nil
	}
	// 양 side 모두 supported 검증
	if !IsSupportedMongoDBVersion(from) {
		return fmt.Errorf("from version %q is not supported", from)
	}
	if !IsSupportedMongoDBVersion(to) {
		return fmt.Errorf("to version %q is not supported", to)
	}

	fromMajor, fromMinor, fromOK := parseMongoVersion(from)
	toMajor, toMinor, toOK := parseMongoVersion(to)
	if !fromOK || !toOK {
		return fmt.Errorf("unable to parse versions: from=%q to=%q", from, to)
	}

	// Major skip 차단 (8.x → 9.x 또는 그 이상의 jump 도 차단)
	if toMajor != fromMajor {
		return fmt.Errorf("major version change %q → %q is not allowed (must upgrade through supported minor path)", from, to)
	}

	// Downgrade 차단
	if toMinor < fromMinor {
		return fmt.Errorf("downgrade %q → %q is not allowed (featureCompatibilityVersion not guaranteed)", from, to)
	}

	// Minor skip 차단 — supported list (8.0/8.2/8.3) 에서 *인접 한 step 만* 허용
	supported := []int{}
	for _, v := range SupportedMongoDBVersions {
		if maj, min, ok := parseMongoVersion(v); ok && maj == fromMajor {
			supported = append(supported, min)
		}
	}
	// supported 가 정렬된 list 라고 가정 (sort 부담 피함 — 작은 set).
	// fromMinor 의 index 와 toMinor 의 index 가 인접해야 함.
	fromIdx, toIdx := -1, -1
	for i, m := range supported {
		if m == fromMinor {
			fromIdx = i
		}
		if m == toMinor {
			toIdx = i
		}
	}
	if fromIdx < 0 || toIdx < 0 {
		return fmt.Errorf("version not in supported minor sequence: from=%q to=%q", from, to)
	}
	if toIdx-fromIdx > 1 {
		return fmt.Errorf("minor skip %q → %q is not allowed (must upgrade through %q)", from, to, formatMongoVersion(fromMajor, supported[fromIdx+1]))
	}

	return nil
}

// parseMongoVersion — "8.2" 또는 "8.2.3" → (8, 2, true). 실패 시 false.
func parseMongoVersion(v string) (major, minor int, ok bool) {
	parts := splitVersion(v)
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, e1 := atoi(parts[0])
	min, e2 := atoi(parts[1])
	if e1 != nil || e2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}

func formatMongoVersion(major, minor int) string {
	return fmt.Sprintf("%d.%d", major, minor)
}

func splitVersion(v string) []string {
	parts := []string{}
	cur := ""
	for _, c := range v {
		if c == '.' {
			parts = append(parts, cur)
			cur = ""
		} else {
			cur += string(c)
		}
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	return parts
}

func atoi(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not digit: %q", c)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
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

// ScalePolicy는 멤버 수 변경의 안전성 가드를 정의한다.
//
// MongoDB ReplicaSet 멤버 수 변경(MongoDB.Spec.Members, ConfigServer.Members)은
// RS reconfig + initial sync 부작용을 동반한다 — 잘못된 spec 변경 한 번에
// PRIMARY 부재 / 수십 분 IO 폭주 / shard rebalancing 동반 진행 가능.
// Deliberate=true가 명시되어야만 즉시 적용되며, 그 외에는 operator가 의도된
// 값을 Status에 기록하고 STS replicas를 변경하지 않는다.
//
// 이 패턴은 MongoDB Inc. operator의 HumanReviewRequired 가드, Percona PSMDB의
// manualUpdate 플래그와 동일한 보호 의도를 가진다.
type ScalePolicy struct {
	// Deliberate가 true이면 멤버 수 변경을 즉시 적용. false/미지정이면 pending.
	// +optional
	Deliberate bool `json:"deliberate,omitempty"`
}

// HPAStatus는 컴포넌트의 HorizontalPodAutoscaler 현재 상태 스냅샷이다.
// reconcile loop이 매 cycle에서 cluster의 HPA 객체를 읽어 CR status에 복사한다.
type HPAStatus struct {
	// Enabled는 HPA가 spec에서 활성화됐는지 여부.
	Enabled bool `json:"enabled"`

	// CurrentReplicas는 HPA controller가 현재 측정한 replica 수.
	// +optional
	CurrentReplicas int32 `json:"currentReplicas,omitempty"`

	// DesiredReplicas는 HPA controller가 metric에 따라 계산한 목표 replica 수.
	// +optional
	DesiredReplicas int32 `json:"desiredReplicas,omitempty"`

	// MinReplicas는 spec의 하한.
	// +optional
	MinReplicas int32 `json:"minReplicas,omitempty"`

	// MaxReplicas는 spec의 상한.
	// +optional
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

	// LastScaleTime은 HPA controller가 마지막으로 scale을 변경한 시각.
	// +optional
	LastScaleTime string `json:"lastScaleTime,omitempty"`
}

// PendingScale은 ScalePolicy.Deliberate=false 가드 때문에 즉시 적용되지 않은
// 멤버 수 변경 요청을 노출한다. 운영자가 `kubectl get -o jsonpath='{.status
// .pendingScale}'`만으로 보류된 변경을 인지하고, deliberate=true로 승인할 수
// 있게 한다.
type PendingScale struct {
	// CurrentMembers는 현재 STS의 replicas.
	CurrentMembers int32 `json:"currentMembers"`

	// DesiredMembers는 spec에서 요청된 새 값.
	DesiredMembers int32 `json:"desiredMembers"`

	// RequestedAt은 spec 변경이 처음 감지된 시각(RFC3339).
	// +optional
	RequestedAt string `json:"requestedAt,omitempty"`

	// Reason은 보류 이유(보통 "ScalePolicy.Deliberate=false").
	// +optional
	Reason string `json:"reason,omitempty"`
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

	// DiagnosticMode 는 진단용 일시 모드. Enabled=true 시 mongod 컨테이너의
	// command 를 `["sleep","infinity"]` 로 덮어쓰고 LivenessProbe / ReadinessProbe /
	// Lifecycle(admin bootstrap) 을 비활성화하여, mongod 가 기동 실패해도 pod 는
	// Running 상태로 유지된다. 운영자는 `kubectl exec` 로 진입해 데이터 디렉터리·
	// 설정·로그를 조사할 수 있다. **운영 데이터셋에서 사용 시 mongod 가 클라이언트
	// 요청을 처리하지 않으므로** 단발 진단 후 즉시 false 로 되돌릴 것.
	// +optional
	DiagnosticMode *DiagnosticModeSpec `json:"diagnosticMode,omitempty"`
}

// DiagnosticModeSpec 은 진단용 컨테이너 override 설정.
type DiagnosticModeSpec struct {
	// Enabled 가 true 면 mongod 컨테이너를 sleep infinity 로 교체한다.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`
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
