/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// ldap.go — F24-F26 (cycle 4) LDAP authentication helper.
//
// mongod `--setParameter authenticationMechanisms` 와 LDAP config 옵션
// 을 합쳐 *mongod 기동 옵션 string list* 를 생성한다. 본 cycle 의 acceptance
// 는 *옵션 list 생성의 결정적 정합* — 실제 LDAP server bind + 인증 round-
// trip 은 cycle 8+ ClusterGroup 단계에서 강화 (real LDAP testkit 필요).

package auth

import (
	"fmt"
	"strings"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// LDAPMongodArgs 는 LDAPSpec 으로부터 mongod CLI args 를 생성한다.
// nil spec → 빈 slice. 빈 slice 는 호출자가 그대로 append 해도 noop.
//
// MongoDB Enterprise 의 LDAP option 패턴:
//
//	--setParameter authenticationMechanisms=PLAIN,SCRAM-SHA-256
//	--setParameter saslHostName=...
//	--ldapServers=ldap1:389,ldap2:389
//	--ldapBindMethod=simple
//	--ldapBindWithOSDefaults=false
//	--ldapTransportSecurity=tls
//	--ldapUserToDNMapping="[{...}]"
//	--ldapAuthzQueryTemplate="{USER}?memberOf?base"
func LDAPMongodArgs(spec *mongodbv1alpha1.LDAPSpec) []string {
	if spec == nil {
		return nil
	}
	if spec.Servers == "" {
		return nil
	}
	args := []string{
		fmt.Sprintf("--ldapServers=%s", spec.Servers),
		fmt.Sprintf("--ldapBindMethod=%s", defaultStr(spec.BindMethod, "simple")),
	}
	if spec.TLS {
		args = append(args, "--ldapTransportSecurity=tls")
	} else {
		args = append(args, "--ldapTransportSecurity=none")
	}
	if spec.UserToDNMapping != "" {
		args = append(args, fmt.Sprintf("--ldapUserToDNMapping=%s", spec.UserToDNMapping))
	}
	if spec.AuthorizationQueryTemplate != "" {
		args = append(args, fmt.Sprintf("--ldapAuthzQueryTemplate=%s", spec.AuthorizationQueryTemplate))
	}
	return args
}

// LDAPCASecretMountPath 는 LDAP CA bundle volume mount path.
const LDAPCASecretMountPath = "/etc/mongodb-ldap-ca"

// LDAPBindSecretMountPath 는 LDAP bind credentials volume mount path.
const LDAPBindSecretMountPath = "/etc/mongodb-ldap-bind"

// ValidateLDAPSpec 은 webhook 검증 hook. 빈 servers / unsafe TLS off 같은
// 사고 패턴 차단.
func ValidateLDAPSpec(spec *mongodbv1alpha1.LDAPSpec) error {
	if spec == nil {
		return nil
	}
	if strings.TrimSpace(spec.Servers) == "" {
		return fmt.Errorf("ldap.servers is required when ldap spec is set")
	}
	// #222: LDAP 설정 시 TLS 활성화 필수. 평문 LDAP 통신은 credential
	// 탈취 위험이 있으므로 webhook 에서 강제 차단.
	if !spec.TLS {
		return fmt.Errorf("ldap.tls must be true when LDAP is configured — plaintext LDAP exposes credentials to network sniffing")
	}
	if spec.BindMethod != "" && spec.BindMethod != "simple" && spec.BindMethod != "sasl" {
		return fmt.Errorf("ldap.bindMethod must be 'simple' or 'sasl' (got %q)", spec.BindMethod)
	}
	return nil
}

func defaultStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
