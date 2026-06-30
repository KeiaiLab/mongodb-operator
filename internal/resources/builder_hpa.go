/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/api/resource"

	commonshpa "github.com/keiailab/keiailab-commons/pkg/hpa"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// BuildMongosHPA는 mongos Deployment에 대한 HorizontalPodAutoscaler를 만든다.
// `Spec.Mongos.AutoScaling.Enabled=false`이면 nil(호출자가 기존 HPA 삭제 처리).
// mongos는 stateless router라 deliberate 가드가 불필요(ADR-0007).
func BuildMongosHPA(mdbsh *mongodbv1alpha1.MongoDBSharded) *autoscalingv2.HorizontalPodAutoscaler {
	if !IsMongosHPAActive(mdbsh) {
		return nil
	}
	return buildHPAForTarget(
		mdbsh.Name+"-mongos-hpa", mdbsh.Namespace, buildLabels(mdbsh.Name, "mongos"),
		"Deployment", mdbsh.Name+"-mongos",
		mdbsh.Spec.Mongos.AutoScaling,
	)
}

// BuildReplicaSetHPA는 ReplicaSet StatefulSet에 대한 HPA를 만든다.
// `Spec.AutoScaling.Enabled=true` + `Spec.ScalePolicy.Deliberate=true` *둘 다*
// 일 때만 nil 아닌 HPA 반환(이중 가드, ADR-0008).
//
// 이중 가드 근거 — RS 멤버 수 변경은 RS reconfig + initial sync 부작용 동반.
// HPA controller가 metric 변동에 따라 Replicas를 자동 patch하면 운영자 모르는
// 사이 RS reconfig가 발동될 수 있어, "운영자 의도된 자동화"임을 명시(deliberate)
// 해야만 활성화한다.
func BuildReplicaSetHPA(mdb *mongodbv1alpha1.MongoDB) *autoscalingv2.HorizontalPodAutoscaler {
	if !IsRSHPAActive(mdb) {
		return nil
	}
	return buildHPAForTarget(
		mdb.Name+"-hpa", mdb.Namespace, buildLabels(mdb.Name, "replicaset"),
		"StatefulSet", mdb.Name,
		mdb.Spec.AutoScaling,
	)
}

// BuildConfigServerHPA는 cfg StatefulSet에 대한 HPA를 만든다. cfg는 보통 작은
// 멤버 수(3-7)이고 변동이 거의 없어 *deliberate 이중 가드*를 동일하게 적용한다
// (ADR-0009).
func BuildConfigServerHPA(mdbsh *mongodbv1alpha1.MongoDBSharded) *autoscalingv2.HorizontalPodAutoscaler {
	if !IsConfigServerHPAActive(mdbsh) {
		return nil
	}
	return buildHPAForTarget(
		mdbsh.Name+"-cfg-hpa", mdbsh.Namespace, buildLabels(mdbsh.Name, "configsvr"),
		"StatefulSet", mdbsh.Name+"-cfg",
		mdbsh.Spec.ConfigServer.AutoScaling,
	)
}

// buildHPAForTarget는 HPA 객체 생성 공통 로직(가드 검사는 호출자 책임).
func buildHPAForTarget(name, namespace string, labels map[string]string, kind, refName string,
	as *mongodbv1alpha1.AutoScalingSpec,
) *autoscalingv2.HorizontalPodAutoscaler {
	// 빌드 골격(ScaleTargetRef apps/v1 + MinReplicas clamp)은
	// keiailab-commons/pkg/hpa.Build 에 위임 (MinFloor=1 = mongo). metric 조립은
	// buildHPAMetrics(cpu/memory/custom) 도메인 잔류.
	return commonshpa.Build(commonshpa.Params{
		Name:        name,
		Namespace:   namespace,
		Labels:      labels,
		TargetKind:  kind,
		TargetName:  refName,
		MinReplicas: as.MinReplicas,
		MaxReplicas: as.MaxReplicas,
		MinFloor:    1,
		Metrics:     buildHPAMetrics(as.Metrics),
	})
}

// IsRSHPAActive는 RS HPA가 reconcile에서 활성 상태인지 검사한다(이중 가드).
// applyStatefulSet의 HPA-aware preserve 분기에서 사용.
func IsRSHPAActive(mdb *mongodbv1alpha1.MongoDB) bool {
	return mdb.Spec.AutoScaling != nil && mdb.Spec.AutoScaling.Enabled &&
		mdb.Spec.ScalePolicy != nil && mdb.Spec.ScalePolicy.Deliberate
}

// IsConfigServerHPAActive는 cfg HPA의 이중 가드 통과 여부를 검사한다.
func IsConfigServerHPAActive(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return mdbsh.Spec.ConfigServer.AutoScaling != nil && mdbsh.Spec.ConfigServer.AutoScaling.Enabled &&
		mdbsh.Spec.ConfigServer.ScalePolicy != nil && mdbsh.Spec.ConfigServer.ScalePolicy.Deliberate
}

// IsMongosHPAActive는 mongos HPA의 활성 상태(단일 가드: enabled).
func IsMongosHPAActive(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return mdbsh.Spec.Mongos.AutoScaling != nil && mdbsh.Spec.Mongos.AutoScaling.Enabled
}

// IsRSScaleDeliberate는 ReplicaSet 멤버 수 변경이 의도된 자동화임을 검사한다.
// Spec.ScalePolicy=nil이면 default false(즉시 변경 금지). true이면 spec.Members
// 변경이 즉시 STS replicas로 반영된다(ADR-0008).
func IsRSScaleDeliberate(mdb *mongodbv1alpha1.MongoDB) bool {
	return mdb.Spec.ScalePolicy != nil && mdb.Spec.ScalePolicy.Deliberate
}

// IsConfigServerScaleDeliberate는 cfg 멤버 수 변경 가드(ADR-0008/0009).
func IsConfigServerScaleDeliberate(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return mdbsh.Spec.ConfigServer.ScalePolicy != nil && mdbsh.Spec.ConfigServer.ScalePolicy.Deliberate
}

// IsShardScaleDeliberate는 shard MembersPerShard 변경 가드(ADR-0008/0009).
func IsShardScaleDeliberate(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	return mdbsh.Spec.Shards.ScalePolicy != nil && mdbsh.Spec.Shards.ScalePolicy.Deliberate
}

// buildHPAMetrics는 spec의 metric 목록을 autoscaling/v2 metric으로 변환한다.
// 알 수 없는 type은 silently skip하지 않고(운영자 진단성을 위해) caller가 spec
// validation에서 차단해야 한다 — 본 함수는 변환 책임만.
func buildHPAMetrics(metrics []mongodbv1alpha1.AutoScalingMetric) []autoscalingv2.MetricSpec {
	if len(metrics) == 0 {
		// 아무 metric도 없으면 cpu 80% 기본값 — upstream chart 등 표준 기본.
		return []autoscalingv2.MetricSpec{commonshpa.CPUUtilization(80)}
	}
	out := make([]autoscalingv2.MetricSpec, 0, len(metrics))
	for _, m := range metrics {
		target := m.Target
		switch m.Type {
		case "cpu":
			out = append(out, commonshpa.CPUUtilization(target))
		case "memory":
			out = append(out, commonshpa.MemoryUtilization(target))
		case "custom":
			if m.CustomMetric == nil || m.CustomMetric.Name == "" {
				continue
			}
			q := resource.NewQuantity(int64(target), resource.DecimalSI)
			out = append(out, autoscalingv2.MetricSpec{
				Type: autoscalingv2.PodsMetricSourceType,
				Pods: &autoscalingv2.PodsMetricSource{
					Metric: autoscalingv2.MetricIdentifier{Name: m.CustomMetric.Name},
					Target: autoscalingv2.MetricTarget{
						Type:         autoscalingv2.AverageValueMetricType,
						AverageValue: q,
					},
				},
			})
		}
	}
	return out
}
