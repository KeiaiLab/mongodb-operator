/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonsnp "github.com/keiailab/keiailab-commons/pkg/networkpolicy"
	commonspdb "github.com/keiailab/keiailab-commons/pkg/pdb"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// BuildMongoDBPDB는 MongoDB ReplicaSet workload를 위한 PodDisruptionBudget을 생성한다.
// spec.podDisruptionBudget가 nil이거나 enabled=false면 nil을 반환해 controller가
// 생성/업데이트를 skip하게 한다.
//
// 기본값: minAvailable = max(replicas-1, 0). 3 멤버 RS면 minAvailable=2가
// 적용되어 한 번에 한 멤버만 maintenance 가능하도록 보장.
func BuildMongoDBPDB(mdb *mongodbv1alpha1.MongoDB) *policyv1.PodDisruptionBudget {
	pdbSpec := mdb.Spec.PodDisruptionBudget
	if pdbSpec == nil || !pdbSpec.Enabled {
		return nil
	}
	labels := buildLabels(mdb.Name, "replicaset")
	// 빌드 골격은 keiailab-commons/pkg/pdb.Build 에 위임 (DefaultFloor=0 = mongo).
	// mongo 는 MinAvailable-우선 = commons 기본과 동일 → 거동 보존.
	return commonspdb.Build(commonspdb.Params{
		Name:           mdb.Name + "-pdb",
		Namespace:      mdb.Namespace,
		Labels:         labels,
		Selector:       labels,
		Replicas:       mdb.Spec.Members,
		DefaultFloor:   0,
		MinAvailable:   pdbSpec.MinAvailable,
		MaxUnavailable: pdbSpec.MaxUnavailable,
	})
}

// BuildMongoDBNetworkPolicy는 MongoDB ReplicaSet pods에 대한 deny-by-default
// NetworkPolicy를 생성한다. spec.networkPolicy.enabled=false면 nil 반환.
//
// 기본 정책: 같은 RS의 pods간 27017 ingress만 허용. AdditionalIngressFrom으로
// 운영 namespace, 모니터링 stack(exporter scrape) 등을 추가 ingress로 명시 가능.
//
// iteration 26 (2026-05-07): keiailab-commons/pkg/networkpolicy v0.3.0 위임.
// 인라인 builder → commons.New + WithSelfIngress + WithIngressFromPeers.
// valkey iteration 25 (97162b5) 패턴 차용. semantic equivalence 보존.
func BuildMongoDBNetworkPolicy(mdb *mongodbv1alpha1.MongoDB) *networkingv1.NetworkPolicy {
	npSpec := mdb.Spec.NetworkPolicy
	if npSpec == nil || !npSpec.Enabled {
		return nil
	}
	selector := buildLabels(mdb.Name, "replicaset")

	opts := []commonsnp.Option{
		commonsnp.WithLabels(selector),
		commonsnp.WithSelfIngress([]int32{int32(mongoDBPort)}),
	}
	if extra := convertAdditionalPeers(npSpec.AdditionalIngressFrom); len(extra) > 0 {
		opts = append(opts, commonsnp.WithIngressFromPeers(extra, []int32{int32(mongoDBPort)}))
	}
	return commonsnp.New(mdb.Name+"-netpol", mdb.Namespace, selector, opts...)
}

// convertAdditionalPeers — mongodbv1alpha1.NetworkPolicyPeer → commons Peer.
// PodSelector / NamespaceSelector 둘 다 nil 인 entry 는 skip (기존 동작 보존).
func convertAdditionalPeers(in []mongodbv1alpha1.NetworkPolicyPeer) []commonsnp.Peer {
	out := make([]commonsnp.Peer, 0, len(in))
	for _, peer := range in {
		if peer.PodSelector == nil && peer.NamespaceSelector == nil {
			continue
		}
		p := commonsnp.Peer{}
		if peer.PodSelector != nil {
			p.PodSelector = *peer.PodSelector
		}
		if peer.NamespaceSelector != nil {
			p.NamespaceSelector = *peer.NamespaceSelector
		}
		out = append(out, p)
	}
	return out
}

// pdbBaseSpec은 PodDisruptionBudgetSpec를 받아 기본 minAvailable=replicas-1을
// 적용한 K8s PDB spec을 만든다(MinAvailable/MaxUnavailable 미지정 시).
func pdbBaseSpec(spec *mongodbv1alpha1.PodDisruptionBudgetSpec, replicas int32, selector map[string]string) policyv1.PodDisruptionBudgetSpec {
	// 빌드 골격(min/max 우선순위 + default-floor)은 keiailab-commons/pkg/pdb.Build
	// 에 위임 (DefaultFloor=0 = mongo). BuildShardedPDBs 가 ObjectMeta 를 별도
	// 설정하므로 여기서는 Spec 만 반환한다.
	return commonspdb.Build(commonspdb.Params{
		Selector:       selector,
		Replicas:       replicas,
		DefaultFloor:   0,
		MinAvailable:   spec.MinAvailable,
		MaxUnavailable: spec.MaxUnavailable,
	}).Spec
}

// BuildShardedPDBs는 sharded cluster의 cfg/shards/mongos 각 컴포넌트에 대한
// PodDisruptionBudget 슬라이스를 반환한다. Spec.PodDisruptionBudget이 nil이거나
// disabled면 nil 슬라이스 반환.
func BuildShardedPDBs(mdbsh *mongodbv1alpha1.MongoDBSharded) []*policyv1.PodDisruptionBudget {
	pdbSpec := mdbsh.Spec.PodDisruptionBudget
	if pdbSpec == nil || !pdbSpec.Enabled {
		return nil
	}
	var out []*policyv1.PodDisruptionBudget

	// Config server
	cfgSelector := buildLabels(mdbsh.Name, "configsvr")
	out = append(out, &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-cfg-pdb",
			Namespace: mdbsh.Namespace,
			Labels:    cfgSelector,
		},
		Spec: pdbBaseSpec(pdbSpec, mdbsh.Spec.ConfigServer.Members, cfgSelector),
	})

	// Shards (각 shard별)
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		shSelector := buildLabels(mdbsh.Name, fmt.Sprintf("shard-%d", i))
		out = append(out, &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-shard-%d-pdb", mdbsh.Name, i),
				Namespace: mdbsh.Namespace,
				Labels:    shSelector,
			},
			Spec: pdbBaseSpec(pdbSpec, mdbsh.Spec.Shards.MembersPerShard, shSelector),
		})
	}

	// Mongos
	mongosSelector := buildLabels(mdbsh.Name, "mongos")
	out = append(out, &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-mongos-pdb",
			Namespace: mdbsh.Namespace,
			Labels:    mongosSelector,
		},
		Spec: pdbBaseSpec(pdbSpec, mdbsh.Spec.Mongos.Replicas, mongosSelector),
	})

	return out
}

// buildShardedComponentNetworkPolicy은 cfg/shard/mongos 컴포넌트별 NetworkPolicy
// 빌더의 공용 helper. selector(컴포넌트 라벨)와 port(cfg=27019/shard=27018/
// mongos=27017)를 매개변수화한다.
//
// iteration 26: commons.New 위임. self-peer 가 *cluster-wide* (instance + managed-by
// 만 매칭, component 무관) — cfg ↔ shard ↔ mongos 통신 허용 패턴 — WithIngressFromPeers
// 의 cluster-wide Peer 로 표현.
func buildShardedComponentNetworkPolicy(mdbsh *mongodbv1alpha1.MongoDBSharded, name string, port int, selector map[string]string) *networkingv1.NetworkPolicy {
	npSpec := mdbsh.Spec.NetworkPolicy
	if npSpec == nil || !npSpec.Enabled {
		return nil
	}
	ports := []int32{int32(port)}
	clusterPeer := commonsnp.Peer{
		PodSelector: map[string]string{
			"app.kubernetes.io/instance":   mdbsh.Name,
			"app.kubernetes.io/managed-by": "mongodb-operator",
		},
	}
	opts := []commonsnp.Option{
		commonsnp.WithLabels(selector),
		commonsnp.WithIngressFromPeers([]commonsnp.Peer{clusterPeer}, ports),
	}
	if extra := convertAdditionalPeers(npSpec.AdditionalIngressFrom); len(extra) > 0 {
		opts = append(opts, commonsnp.WithIngressFromPeers(extra, ports))
	}
	return commonsnp.New(name, mdbsh.Namespace, selector, opts...)
}

// BuildShardedNetworkPolicies는 cfg/shards/mongos 각 컴포넌트에 대한 NetworkPolicy
// 슬라이스를 반환한다. spec.networkPolicy.enabled=false면 nil 반환.
func BuildShardedNetworkPolicies(mdbsh *mongodbv1alpha1.MongoDBSharded) []*networkingv1.NetworkPolicy {
	npSpec := mdbsh.Spec.NetworkPolicy
	if npSpec == nil || !npSpec.Enabled {
		return nil
	}
	var out []*networkingv1.NetworkPolicy
	out = append(out, buildShardedComponentNetworkPolicy(mdbsh, mdbsh.Name+"-cfg-netpol", 27019,
		buildLabels(mdbsh.Name, "configsvr")))
	for i := int32(0); i < mdbsh.Spec.Shards.Count; i++ {
		out = append(out, buildShardedComponentNetworkPolicy(mdbsh,
			fmt.Sprintf("%s-shard-%d-netpol", mdbsh.Name, i), 27018,
			buildLabels(mdbsh.Name, fmt.Sprintf("shard-%d", i))))
	}
	out = append(out, buildShardedComponentNetworkPolicy(mdbsh, mdbsh.Name+"-mongos-netpol", 27017,
		buildLabels(mdbsh.Name, "mongos")))
	return out
}
