/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package security

import (
	"fmt"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// ValidateNetworkPolicyPeers 는 NetworkPolicy 빌더가 *조용히 drop* 하거나
// 과도하게 넓게 적용할 additionalIngressFrom peer 를 surface 한다.
//
//   - PodSelector·NamespaceSelector 둘 다 nil 인 peer 는 아무 것도 매칭하지
//     않아 빌드 시 skip 되며(resources.convertAdditionalPeers), 사용자가 선언한
//     ingress 규칙이 피드백 없이 사라진다 → Error.
//   - 빈 PodSelector(+nil NamespaceSelector)는 정책 네임스페이스의 전체 pod 를
//     허용해 과도하게 넓다 → Warning.
//
// 순수 함수 — k8s 호출 0, 결정적 순서(입력 슬라이스 순서 보존). nil/빈 입력은
// nil 반환.
func ValidateNetworkPolicyPeers(peers []mongodbv1alpha1.NetworkPolicyPeer) []Finding {
	var out []Finding
	for i, peer := range peers {
		field := fmt.Sprintf("spec.networkPolicy.additionalIngressFrom[%d]", i)
		switch {
		case peer.PodSelector == nil && peer.NamespaceSelector == nil:
			out = append(out, Finding{
				Field:    field,
				Reason:   "podSelector·namespaceSelector 둘 다 미설정 — peer 가 아무 것도 매칭하지 않아 drop 됨",
				Severity: SeverityError,
			})
		case peer.PodSelector != nil && len(*peer.PodSelector) == 0 && peer.NamespaceSelector == nil:
			out = append(out, Finding{
				Field:    field,
				Reason:   "빈 podSelector + namespaceSelector 미설정 — 네임스페이스 전체 pod ingress 허용(과도하게 넓음)",
				Severity: SeverityWarning,
			})
		}
	}
	return out
}
