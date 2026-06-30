/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	corev1 "k8s.io/api/core/v1"

	commonsvolume "github.com/keiailab/keiailab-commons/pkg/volume"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// BuildPEMMergeInitContainer 는 cert-manager Secret 의 tls.crt + tls.key 를
// 단일 PEM file 로 합치는 init container 를 반환한다 (mongod 의 --tlsCertificateKeyFile
// 호환). PSA restricted 정합 (busybox + RunAsUser 999 + drop ALL + seccomp RuntimeDefault).
//
// Ownership 은 root chown 이 아니라 PodSecurityContext.FSGroup (=999,
// buildDefaultSecurityContext) 에 위임한다. fsGroup 이 emptyDir `/tls-pem` 을
// gid 999 로 group-writable 하게 만들어 UID 999 init container 가 server.pem 을
// 직접 write → file owner 999:999 → mongod (UID 999) read 가능. copy-keyfile init
// 과 동일 패턴 (buildKeyfileInitContainerSecurityContext).
//
// 회귀 주의: 과거 RunAsUser:0 + CHOWN cap 구현 (2026-05-16 chown race 오진) 은
// PSA restricted enforce namespace 에서 pod 생성 거부 (CHOWN cap / runAsUser=0 /
// seccomp 누락) → StatefulSet rollout 영구 차단 (2026-05-30 KeiaiLab data ns 사고).
// fsGroup 기반이 정합 (라이브 검증: data ns restricted enforce + cfg/shard/mongos 3/3).
//
// Exported — caller (cfg/shard/mongos reconciler) 가 STS build 후 conditional append.
func BuildPEMMergeInitContainer() corev1.Container {
	return corev1.Container{
		Name:  "tls-pem-merge",
		Image: keyfileInitImage, // busybox:1.37 const 단일화 정합
		Command: []string{
			"sh", "-c",
			// 멱등(idempotent) — pod/init 재시작 시 emptyDir 의 기존 server.pem(0400)을
			// `> server.pem` 으로 덮어쓰면 owner write 권한 부재로 "Permission denied" 크래시.
			// (실 사고 2026-06-22: node/kubelet 재기동 후 init 재실행 → tls-pem-merge
			// CrashLoopBackOff → pod not-ready → 클러스터 회복 정체.) temp 파일에 쓴 뒤
			// atomic `mv -f` 로 교체 — rename 은 dir write(fsGroup=999)만 필요, 대상 파일 0400
			// 무관. .tmp 는 chmod 하지 않아 재실행 시 항상 overwrite 가능.
			"cat /tls-input/tls.crt /tls-input/tls.key > /tls-pem/server.pem.tmp && " +
				"mv -f /tls-pem/server.pem.tmp /tls-pem/server.pem && " +
				"chmod 0400 /tls-pem/server.pem",
		},
		SecurityContext: buildKeyfileInitContainerSecurityContext(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "tls-server", MountPath: "/tls-input", ReadOnly: true},
			{Name: "tls-server-pem", MountPath: "/tls-pem"},
		},
	}
}

// buildTLSPEMVolume 는 init container 와 mongod 가 공유하는 emptyDir.
func buildTLSPEMVolume() corev1.Volume {
	return corev1.Volume{
		Name:         "tls-server-pem",
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}
}

// buildTLSPEMMount 는 mongod container 의 mount.
func buildTLSPEMMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      "tls-server-pem",
		MountPath: MongoTLSPEMPath,
		ReadOnly:  true,
	}
}

// tlsArgs 는 mongod TLS 활성 args. preferTLS mode = plaintext + TLS 양쪽 listen
// → operator client (keyfile + plaintext) 와 외부 TLS client 양쪽 호환. cycle 19
// Phase 3b 의 *진정 무중단* 정공.
//
// tlsAllowConnectionsWithoutCertificates: client cert 없는 plaintext connection 허용
// (mTLS 비강제). cycle 19 valkey-operator 의 tls-auth-clients=yes 강제 패턴 회피.
//
// tlsAllowInvalidHostnames (v1.4.17 fix, cycle 19 last): mongos 가 cfg replica set 에
// outbound TLS connect 시 hostname verification 을 우회. mongos --configdb 의 connection
// string 은 short hostname (keiailab-mongo-cfg-0:27017) 사용 — cert SAN 의 wildcard FQDN
// (*.keiailab-mongo-cfg-headless.<ns>.svc.cluster.local) 와 직접 매치 안 됨 → TLS verify
// fail → sharding pool init 실패 → 27017 미 listen → kubelet liveness kill cascade.
// cluster-internal CA chain + preferTLS 환경에서 hostname 검증은 의미 적음 (CA 로 ID
// 검증 충분), short/long hostname mix 흡수 의무.
func tlsArgs(tls *mongodbv1alpha1.TLSSpec) []string {
	mode := "preferTLS"
	if tls != nil && tls.Mode != "" {
		mode = tls.Mode
	}
	args := []string{
		"--tlsMode", mode,
		"--tlsCertificateKeyFile", MongoTLSPEMPath + "/server.pem",
		"--tlsCAFile", MongoTLSMountPath + "/ca.crt",
		"--tlsAllowConnectionsWithoutCertificates",
	}
	if tls == nil || tls.AllowInvalidHostnames == nil || *tls.AllowInvalidHostnames {
		args = append(args, "--tlsAllowInvalidHostnames")
	}
	return args
}

// buildTLSServerVolume 은 cert-manager 가 발급한 server cert Secret (<name>-tls)
// 을 mount 하는 Volume 을 반환한다. tlsEnabled=false 시 nil.
//
// defaultMode 0o400: mongo user read-only. mongod 의 키 파일 권한 검사 통과.
// Phase 3a 는 volume mount 만 추가 — mongod args 의 --tlsCertificateKeyFile 통합은
// Phase 3b 에서 init container PEM merge (cat tls.crt tls.key > server.pem) 후.
func buildTLSServerVolume(secretName string) corev1.Volume {
	// 0o400/readonly cert 불변식은 keiailab-commons/pkg/volume.TLSSecretMount 에 위임.
	vol, _ := commonsvolume.TLSSecretMount("tls-server", secretName, MongoTLSMountPath)
	return vol
}

// buildTLSServerMount 는 tls-server Volume 의 mountPath 를 반환한다.
func buildTLSServerMount() corev1.VolumeMount {
	_, mount := commonsvolume.TLSSecretMount("tls-server", "", MongoTLSMountPath)
	return mount
}
