/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// BuildMongoDBConfigMap creates a ConfigMap for MongoDB configuration.
//
// 포함 스크립트:
//   - readiness-probe.sh: mongod이 ping에 응답하는지 확인.
//   - bootstrap-admin.sh: pod 자체가 자기 mongod에 localhost connection으로
//     첫 admin user를 생성. operator는 더 이상 pods/exec을 수행하지 않는다.
func BuildMongoDBConfigMap(mdb *mongodbv1alpha1.MongoDB) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdb.Name + "-scripts",
			Namespace: mdb.Namespace,
			Labels:    buildLabels(mdb.Name, "scripts"),
		},
		Data: map[string]string{
			scriptReadiness: buildReadinessScript(mongoDBPort),
			scriptBootstrap: buildAdminBootstrapScript(mongoDBPort),
			scriptStepDown:  buildStepDownScript(mongoDBPort),
		},
	}
}

// BuildCustomConfigMap generates a ConfigMap from spec.pod.customConfig.configInline.
// Returns nil if customConfig is nil or configInline is empty.
func BuildCustomConfigMap(name, namespace string, pod *mongodbv1alpha1.PodSpec) *corev1.ConfigMap {
	if pod == nil || pod.CustomConfig == nil || pod.CustomConfig.ConfigInline == "" {
		return nil
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-custom-config",
			Namespace: namespace,
			Labels:    buildLabels(name, "custom-config"),
		},
		Data: map[string]string{
			"mongod.conf": pod.CustomConfig.ConfigInline,
		},
	}
}

// BuildConfigServerScriptsConfigMap는 Config Server StatefulSet에 마운트되는
// scripts ConfigMap을 만든다. port=27019.
func BuildConfigServerScriptsConfigMap(mdbsh *mongodbv1alpha1.MongoDBSharded) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mdbsh.Name + "-cfg-scripts",
			Namespace: mdbsh.Namespace,
			Labels:    buildLabels(mdbsh.Name, "configsvr"),
		},
		Data: map[string]string{
			scriptReadiness: buildReadinessScript(27019),
			scriptBootstrap: buildAdminBootstrapScript(27019),
			scriptStepDown:  buildStepDownScript(27019),
		},
	}
}

// BuildShardScriptsConfigMap는 Shard StatefulSet에 마운트되는 scripts ConfigMap을
// 만든다. port=27018, name={instance}-shard-{i}-scripts.
func BuildShardScriptsConfigMap(mdbsh *mongodbv1alpha1.MongoDBSharded, shardIndex int32) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-shard-%d-scripts", mdbsh.Name, shardIndex),
			Namespace: mdbsh.Namespace,
			Labels:    buildLabels(mdbsh.Name, fmt.Sprintf("shard-%d", shardIndex)),
		},
		Data: map[string]string{
			scriptReadiness: buildReadinessScript(27018),
			scriptBootstrap: buildAdminBootstrapScript(27018),
			scriptStepDown:  buildStepDownScript(27018),
		},
	}
}
