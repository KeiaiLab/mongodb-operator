/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package security

import (
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func selPtr(m map[string]string) *map[string]string { return &m }

func TestValidateNetworkPolicyPeers_EmptyReturnsNil(t *testing.T) {
	if f := ValidateNetworkPolicyPeers(nil); f != nil {
		t.Errorf("빈 입력 → nil 기대, got %v", f)
	}
}

func TestValidateNetworkPolicyPeers_ValidPeersNoFindings(t *testing.T) {
	peers := []mongodbv1alpha1.NetworkPolicyPeer{
		{PodSelector: selPtr(map[string]string{"app": "x"})},
		{NamespaceSelector: selPtr(map[string]string{"team": "y"})},
		{PodSelector: selPtr(map[string]string{"app": "z"}), NamespaceSelector: selPtr(map[string]string{"team": "w"})},
	}
	if f := ValidateNetworkPolicyPeers(peers); len(f) != 0 {
		t.Errorf("valid peers → 0 findings 기대, got %v", f)
	}
}

func TestValidateNetworkPolicyPeers_BothNilIsError(t *testing.T) {
	f := ValidateNetworkPolicyPeers([]mongodbv1alpha1.NetworkPolicyPeer{{}})
	if len(f) != 1 || f[0].Severity != SeverityError {
		t.Fatalf("both-nil → 1 Error 기대, got %v", f)
	}
	if f[0].Field != "spec.networkPolicy.additionalIngressFrom[0]" {
		t.Errorf("Field 경로 불일치: %s", f[0].Field)
	}
}

func TestValidateNetworkPolicyPeers_EmptyPodSelectorIsWarning(t *testing.T) {
	f := ValidateNetworkPolicyPeers([]mongodbv1alpha1.NetworkPolicyPeer{{PodSelector: selPtr(map[string]string{})}})
	if len(f) != 1 || f[0].Severity != SeverityWarning {
		t.Fatalf("empty podSelector → 1 Warning 기대, got %v", f)
	}
}

func TestValidateNetworkPolicyPeers_IndexPreservedAcrossValidPeer(t *testing.T) {
	peers := []mongodbv1alpha1.NetworkPolicyPeer{
		{PodSelector: selPtr(map[string]string{"app": "ok"})}, // index 0 — valid
		{}, // index 1 — error
	}
	f := ValidateNetworkPolicyPeers(peers)
	if len(f) != 1 || f[0].Field != "spec.networkPolicy.additionalIngressFrom[1]" {
		t.Fatalf("index 1 finding 기대, got %v", f)
	}
}
