/*
Copyright 2026 Keiailab.
*/

// kms.go — F38-F42 (cycle 6) KMS encryption-at-rest helpers.
//
// 본 cycle 의 acceptance: EncryptionSpec validation + provider 별 mongod
// 옵션 생성 + 키 회전 helper signature. 실 KMS provider call (Vault Transit /
// AWS KMS / GCP KMS / Azure KV) 은 cycle 9+ 운영 강화 시점에서 SDK 통합.

package encryption

import (
	"fmt"
	"strings"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	// EncryptionKeyMountPath 는 mongod 가 encryption key file 을 읽는 경로.
	EncryptionKeyMountPath = "/etc/mongodb-encryption"

	// EncryptionKeyFileName 은 KeyProvider=secret 시 mount 되는 keyfile 이름.
	EncryptionKeyFileName = "keyfile"

	providerSecret = "secret"
	providerVault  = "vault"
	providerAWSKMS = "aws-kms"
	providerGCPKMS = "gcp-kms"
	providerAzure  = "azure-kv"
)

// MongodArgs 는 EncryptionSpec 으로부터 mongod CLI args 를 생성한다.
// Enabled=false 또는 nil → 빈 slice (호출자 append noop).
//
// MongoDB Enterprise 의 encryption 옵션:
//
//	--enableEncryption
//	--encryptionCipherMode AES256-GCM
//	--encryptionKeyFile /etc/mongodb-encryption/keyfile      (secret provider)
//	--kmipServerName ...                                     (vault/AWS/GCP/Azure proxy via KMIP)
func MongodArgs(spec *mongodbv1alpha1.EncryptionSpec) []string {
	if spec == nil || !spec.Enabled {
		return nil
	}
	args := []string{"--enableEncryption"}
	cipher := spec.CipherMode
	if cipher == "" {
		cipher = "AES256-GCM"
	}
	args = append(args, fmt.Sprintf("--encryptionCipherMode=%s", cipher))

	switch spec.KeyProvider {
	case "", providerSecret:
		args = append(args, fmt.Sprintf("--encryptionKeyFile=%s/%s", EncryptionKeyMountPath, EncryptionKeyFileName))
	case providerVault, providerAWSKMS, providerGCPKMS, providerAzure:
		// KMIP proxy 통합 — cycle 9+ 에 sidecar 가 KMIP 서버를 노출.
		args = append(args, "--kmipServerName=localhost", "--kmipPort=5696")
	}
	return args
}

// ValidateEncryptionSpec — webhook 검증 hook.
func ValidateEncryptionSpec(spec *mongodbv1alpha1.EncryptionSpec) error {
	if spec == nil || !spec.Enabled {
		return nil
	}
	provider := spec.KeyProvider
	if provider == "" {
		provider = providerSecret
	}
	cfg := spec.KMSConfig
	switch provider {
	case providerSecret:
		if cfg == nil || cfg.Secret == nil || cfg.Secret.SecretRef.Name == "" {
			return fmt.Errorf("encryption.kmsConfig.secret.secretRef is required when keyProvider=secret")
		}
	case providerVault:
		if cfg == nil || cfg.Vault == nil {
			return fmt.Errorf("encryption.kmsConfig.vault is required when keyProvider=vault")
		}
		if strings.TrimSpace(cfg.Vault.Address) == "" {
			return fmt.Errorf("encryption.kmsConfig.vault.address is required")
		}
		if cfg.Vault.KeyName == "" {
			return fmt.Errorf("encryption.kmsConfig.vault.keyName is required")
		}
		if cfg.Vault.AuthSecretRef.Name == "" {
			return fmt.Errorf("encryption.kmsConfig.vault.authSecretRef is required")
		}
	case providerAWSKMS:
		if cfg == nil || cfg.AWSKMS == nil {
			return fmt.Errorf("encryption.kmsConfig.awsKMS is required when keyProvider=aws-kms")
		}
		if !strings.HasPrefix(cfg.AWSKMS.KeyARN, "arn:aws:kms:") {
			return fmt.Errorf("encryption.kmsConfig.awsKMS.keyARN must be a KMS CMK ARN")
		}
	case providerGCPKMS:
		if cfg == nil || cfg.GCPKMS == nil {
			return fmt.Errorf("encryption.kmsConfig.gcpKMS is required when keyProvider=gcp-kms")
		}
		if cfg.GCPKMS.ProjectID == "" || cfg.GCPKMS.Keyring == "" || cfg.GCPKMS.Key == "" {
			return fmt.Errorf("encryption.kmsConfig.gcpKMS requires projectID, keyring, and key")
		}
	case providerAzure:
		if cfg == nil || cfg.AzureKV == nil {
			return fmt.Errorf("encryption.kmsConfig.azureKV is required when keyProvider=azure-kv")
		}
		if !strings.HasPrefix(cfg.AzureKV.VaultURL, "https://") {
			return fmt.Errorf("encryption.kmsConfig.azureKV.vaultURL must start with https")
		}
	default:
		return fmt.Errorf("encryption.keyProvider unknown: %q", provider)
	}
	if spec.KeyRotationDays < 0 {
		return fmt.Errorf("encryption.keyRotationDays must be >= 0")
	}
	return nil
}

// NeedsKeyRotation 은 마지막 회전 시점으로부터 RotationDays 가 경과했는지 검사.
// controller 가 매 reconcile 마다 본 함수를 호출하여 회전 trigger 판단.
// 입력: 마지막 회전 시점 (UnixSeconds), 현재 시점 (UnixSeconds), spec.
func NeedsKeyRotation(lastRotationUnix, nowUnix int64, spec *mongodbv1alpha1.EncryptionSpec) bool {
	if spec == nil || !spec.Enabled || spec.KeyRotationDays <= 0 {
		return false
	}
	secondsPerDay := int64(24 * 60 * 60)
	return nowUnix-lastRotationUnix >= int64(spec.KeyRotationDays)*secondsPerDay
}
