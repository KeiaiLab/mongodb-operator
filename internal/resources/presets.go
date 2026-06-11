/*
Copyright 2026 Keiailab.
*/

// presets.go — F73 (cycle 10) upstream chart 호환 resource presets.

package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// ResourcePreset 은 PodSpec.ResourcesPreset string 에 대응하는 사전 정의
// 리소스 묶음. upstream chart 의 `resources.preset` 와 동일 명명.
//
// 사용자가 직접 Resources 를 지정하면 *preset 무시* — caller (builder) 가
// Resources 우선 확인 후 비어있을 때만 본 함수 호출.
func ResourcePreset(name string) corev1.ResourceRequirements {
	switch name {
	case "nano":
		return mkResources("100m", "128Mi", "200m", "256Mi")
	case "micro":
		return mkResources("250m", "256Mi", "500m", "512Mi")
	case "small":
		return mkResources("500m", "512Mi", "1", "1Gi")
	case "medium":
		return mkResources("1", "1Gi", "2", "2Gi")
	case "large":
		return mkResources("2", "2Gi", "4", "4Gi")
	case "xlarge":
		return mkResources("4", "4Gi", "8", "8Gi")
	case "2xlarge":
		return mkResources("8", "8Gi", "16", "16Gi")
	case "", "none":
		return corev1.ResourceRequirements{}
	}
	return corev1.ResourceRequirements{}
}

func mkResources(cpuReq, memReq, cpuLim, memLim string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpuReq),
			corev1.ResourceMemory: resource.MustParse(memReq),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpuLim),
			corev1.ResourceMemory: resource.MustParse(memLim),
		},
	}
}

// IsValidPreset — webhook validation hook.
func IsValidPreset(name string) bool {
	switch name {
	case "", "none", "nano", "micro", "small", "medium", "large", "xlarge", "2xlarge":
		return true
	}
	return false
}
