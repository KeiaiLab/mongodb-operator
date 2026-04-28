/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"os"
	"path/filepath"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

// TestRBAC_RoleYAML_HasRequiredCorePermissions는 config/rbac/role.yaml이
// controller가 reconcile 중 사용하는 core 자원 권한을 회귀 없이 유지하는지
// 단언한다. kubebuilder marker가 누군가에 의해 삭제되거나 `make manifests`
// 미실행으로 role.yaml이 stale해지면 reconcile이 RBAC 권한 부족으로 실패한다.
//
// 회귀 시나리오:
//   - core/pods get/list/watch 누락 → headless DNS pod 발견 실패
//   - core/secrets get/list/watch 누락 → admin password fetch 실패
//   - 누군가가 pods/exec verb를 추가 → operator는 exec 호출하지 않으므로
//     불필요한 권한 확장 (least privilege 위반). 마커 추가 시 본 테스트의
//     ALLOW 리스트에 명시 후 화이트리스팅해야 함.
func TestRBAC_RoleYAML_HasRequiredCorePermissions(t *testing.T) {
	role := loadClusterRole(t)

	// (group, resource) → required verbs 집합
	required := map[string]map[string][]string{
		"": {
			"pods":       {"get", "list", "watch"},
			"secrets":    {"get", "list", "watch"},
			"services":   {"get", "list", "watch"},
			"configmaps": {"get", "list", "watch"},
		},
		// 본 사이클에서 추가된 권한 — Track A (Lease 분산락) + Track C1/C4 (PDB·NetworkPolicy).
		// kubebuilder 마커가 실수로 제거되어 controller-gen이 role.yaml에서 빼는
		// 회귀를 catch한다.
		"coordination.k8s.io": {
			"leases": {"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		"policy": {
			"poddisruptionbudgets": {"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		"networking.k8s.io": {
			"networkpolicies": {"get", "list", "watch", "create", "update", "patch", "delete"},
		},
	}

	for group, resources := range required {
		for resource, verbs := range resources {
			for _, verb := range verbs {
				if !ruleAllows(role.Rules, group, resource, verb) {
					t.Errorf("ClusterRole에 [group=%q resource=%q verb=%q] 권한 누락 — make manifests 재실행 또는 marker 점검 필요", group, resource, verb)
				}
			}
		}
	}
}

// TestRBAC_RoleYAML_NoUnnecessaryPodExec는 operator가 현재 pod exec를
// 사용하지 않음을 회귀 가드로 단언한다. 누군가 무심코 pods/exec 마커를
// 추가하면 본 테스트가 실패한다. exec를 진짜 도입하는 경우, 본 테스트의
// 의도를 반대로 뒤집어 (a) 코드에 exec 호출 추가, (b) 마커 추가, (c)
// 본 테스트를 "exec verb 존재함" 단언으로 변환하는 3단계가 필요.
func TestRBAC_RoleYAML_NoUnnecessaryPodExec(t *testing.T) {
	role := loadClusterRole(t)
	if ruleAllows(role.Rules, "", "pods/exec", "create") {
		t.Error("ClusterRole에 pods/exec create 권한 존재 — 코드에서 실제 exec 호출이 있는지 확인하고, 없다면 RBAC 마커를 제거해야 함 (least privilege)")
	}
}

func loadClusterRole(t *testing.T) *rbacv1.ClusterRole {
	t.Helper()
	// repo root 기준 상대경로. 테스트는 internal/controller에서 실행되므로 ../..
	path := filepath.Join("..", "..", "config", "rbac", "role.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("role.yaml 읽기 실패: %v", err)
	}
	role := &rbacv1.ClusterRole{}
	if err := yaml.Unmarshal(data, role); err != nil {
		t.Fatalf("role.yaml 파싱 실패: %v", err)
	}
	if role.Name == "" {
		t.Fatal("role.yaml에서 ClusterRole metadata.name이 비어있음")
	}
	return role
}

// ruleAllows는 PolicyRule 슬라이스에서 (group, resource, verb)를 허용하는
// rule이 하나라도 있는지 확인한다. APIGroup 빈 문자열("")은 core API group.
func ruleAllows(rules []rbacv1.PolicyRule, group, resource, verb string) bool {
	for _, r := range rules {
		if !contains(r.APIGroups, group) {
			continue
		}
		if !contains(r.Resources, resource) {
			continue
		}
		if !contains(r.Verbs, verb) {
			continue
		}
		return true
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
