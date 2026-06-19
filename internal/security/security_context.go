/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package security

import (
	corev1 "k8s.io/api/core/v1"

	commonssecurity "github.com/keiailab/keiailab-commons/pkg/security"
)

// mongoUserGroup 은 mongod/mongos 컨테이너의 non-root uid/gid(이미지 빌드 시
// 999 로 고정). PodSecurity restricted 정책을 만족시키기 위한 단일 진실원.
const mongoUserGroup int64 = 999

// DefaultPodSecurityContext 는 operator 의 restricted pod-level SecurityContext
// (non-root 999, RuntimeDefault seccomp)를 반환한다. resources.buildDefaultSecurityContext
// 에 인라인돼 있던 선택을 본 패키지로 위임 — 보안 기본값 단일 진실원.
func DefaultPodSecurityContext() *corev1.PodSecurityContext {
	return commonssecurity.RestrictedPodSecurityContext(
		commonssecurity.WithPodFSGroup(mongoUserGroup),
		commonssecurity.WithPodRunAsUser(mongoUserGroup),
		commonssecurity.WithPodRunAsGroup(mongoUserGroup),
	)
}

// DefaultContainerSecurityContext 는 mongod/mongos 컨테이너용 restricted
// SecurityContext 를 반환한다. read-only rootfs 는 false(mongod 가 런타임에
// 설정 파일/소켓 쓰기 필요).
func DefaultContainerSecurityContext() *corev1.SecurityContext {
	return commonssecurity.RestrictedContainer(
		commonssecurity.WithRunAsUser(mongoUserGroup),
		commonssecurity.WithReadOnlyRootFilesystem(false),
	)
}

// KeyfileInitSecurityContext 는 keyfile 복사 init container(replicaset / config
// server / shard / mongos)용 restricted SecurityContext 를 반환한다.
func KeyfileInitSecurityContext() *corev1.SecurityContext {
	return commonssecurity.RestrictedContainer(
		commonssecurity.WithRunAsUser(mongoUserGroup),
		commonssecurity.WithRunAsGroup(mongoUserGroup),
	)
}

// PreflightContainerSecurityContext 는 사용자가 spec 에 지정한 container
// SecurityContext override 가 restricted baseline 을 약화시키는지 검사한다.
// 순수 함수 — Finding 만 반환하고 적용/차단은 호출자 책임. sc 가 nil 이면 nil.
func PreflightContainerSecurityContext(sc *corev1.SecurityContext) []Finding {
	if sc == nil {
		return nil
	}
	var out []Finding
	if sc.Privileged != nil && *sc.Privileged {
		out = append(out, Finding{
			Field:    "containerSecurityContext.privileged",
			Reason:   "privileged=true 는 restricted baseline 을 깬다",
			Severity: SeverityError,
		})
	}
	if sc.RunAsUser != nil && *sc.RunAsUser == 0 {
		out = append(out, Finding{
			Field:    "containerSecurityContext.runAsUser",
			Reason:   "runAsUser=0(root)은 restricted baseline 을 깬다",
			Severity: SeverityError,
		})
	}
	if sc.RunAsNonRoot != nil && !*sc.RunAsNonRoot {
		out = append(out, Finding{
			Field:    "containerSecurityContext.runAsNonRoot",
			Reason:   "runAsNonRoot=false 는 root 실행을 허용 — restricted baseline 은 non-root 요구",
			Severity: SeverityWarning,
		})
	}
	if sc.AllowPrivilegeEscalation != nil && *sc.AllowPrivilegeEscalation {
		out = append(out, Finding{
			Field:    "containerSecurityContext.allowPrivilegeEscalation",
			Reason:   "allowPrivilegeEscalation=true 는 restricted baseline 을 약화",
			Severity: SeverityWarning,
		})
	}
	return out
}
