/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	"fmt"
	"net/url"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	memberExporterSecretSuffix = "-member-exporter-uri"
	// MemberExporterShardKey / MemberExporterConfigKey are the URI keys of
	// the member exporter Secret (shard mongod 27018, config server 27019).
	MemberExporterShardKey  = "shard"
	MemberExporterConfigKey = "configsvr"
	memberExporterUser      = "admin"
	// exporterContainer / exporterCompatFlag are shared with the RS and mongos
	// exporter sidecars.
	exporterContainer  = "exporter"
	exporterCompatFlag = "--compatible-mode"

	// Member exporter resources when spec.monitoring.exporter.resources
	// leaves the entry unset.
	memberExporterCPURequest    = "50m"
	memberExporterMemoryRequest = "64Mi"
	memberExporterCPULimit      = "200m"
	memberExporterMemoryLimit   = "256Mi"
)

// memberExporterArgs: replica set state and lag (replicasetstatus), oplog
// head/tail = window (diagnosticdata), fsUsedSize/fsTotalSize (dbstats).
// No collstats/indexstats: they walk every collection on each scrape.
var memberExporterArgs = []string{
	"--collector.replicasetstatus",
	"--collector.diagnosticdata",
	"--collector.dbstats",
	exporterCompatFlag,
}

// MemberExportersEnabled reports whether shard and cfg pods carry the exporter.
func MemberExportersEnabled(mdbsh *mongodbv1alpha1.MongoDBSharded) bool {
	m := mdbsh.Spec.Monitoring
	return m != nil && m.Members == mongodbv1alpha1.MemberMonitoringEnabled
}

// MemberExporterSecretName is the Secret holding the localhost URIs.
func MemberExporterSecretName(instanceName string) string {
	return instanceName + memberExporterSecretSuffix
}

// BuildMemberExporterSecret holds one localhost directConnection URI per
// member kind, so each exporter reads only its own mongod.
func BuildMemberExporterSecret(mdbsh *mongodbv1alpha1.MongoDBSharded, password string) *corev1.Secret {
	user := url.UserPassword(memberExporterUser, password).String()
	uri := func(port int) string {
		return fmt.Sprintf("mongodb://%s@localhost:%d/?authSource=admin&directConnection=true", user, port)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      MemberExporterSecretName(mdbsh.Name),
			Namespace: mdbsh.Namespace,
			Labels:    buildLabels(mdbsh.Name, "member-exporter"),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			MemberExporterShardKey:  []byte(uri(MongodShardPort)),
			MemberExporterConfigKey: []byte(uri(27019)),
		},
	}
}

// applyMemberExporter appends the exporter sidecar. Disabled leaves the set
// unchanged.
func applyMemberExporter(sts *appsv1.StatefulSet, mdbsh *mongodbv1alpha1.MongoDBSharded, key string) {
	if !MemberExportersEnabled(mdbsh) {
		return
	}

	spec := &sts.Spec.Template.Spec
	spec.Containers = append(spec.Containers, corev1.Container{
		Name:  exporterContainer,
		Image: exporterImage,
		Args:  memberExporterArgs,
		Ports: []corev1.ContainerPort{{Name: metricsPortName, ContainerPort: metricsPort}},
		Env: []corev1.EnvVar{{
			Name: envMongoDBURI,
			ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: MemberExporterSecretName(mdbsh.Name)},
				Key:                  key,
			}},
		}},
		Resources:       memberExporterResources(mdbsh.Spec.Monitoring.Exporter),
		SecurityContext: buildDefaultContainerSecurityContext(),
	})
}

// memberExporterResources overlays spec.monitoring.exporter.resources on the
// built-in values, one entry at a time.
//
//	spec                    result
//	(unset)                 requests 50m/64Mi   limits 200m/256Mi
//	limits.cpu: 1           requests 50m/64Mi   limits 1/256Mi
//	limits.cpu: 0           requests 50m/64Mi   limits -/256Mi
//	limits.cpu: 30m         requests 30m/64Mi   limits 30m/256Mi
//	requests.memory: 1Gi    requests 50m/1Gi    limits 200m/1Gi
func memberExporterResources(exporter *mongodbv1alpha1.ExporterSpec) corev1.ResourceRequirements {
	res := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(memberExporterCPURequest),
			corev1.ResourceMemory: resource.MustParse(memberExporterMemoryRequest),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(memberExporterCPULimit),
			corev1.ResourceMemory: resource.MustParse(memberExporterMemoryLimit),
		},
	}
	if exporter == nil {
		return res
	}

	for name, q := range exporter.Resources.Requests {
		res.Requests[name] = q.DeepCopy()
	}

	// A zero limit means no limit: the entry is dropped, not rendered as 0.
	for name, q := range exporter.Resources.Limits {
		if q.IsZero() {
			delete(res.Limits, name)
			continue
		}
		res.Limits[name] = q.DeepCopy()
	}

	// The API server rejects a request above its limit. A limit from the spec
	// lowers the request; a built-in limit rises to the request from the spec.
	for name, limit := range res.Limits {
		req, ok := res.Requests[name]
		if !ok || req.Cmp(limit) <= 0 {
			continue
		}
		if _, set := exporter.Resources.Limits[name]; set {
			res.Requests[name] = limit.DeepCopy()
		} else {
			res.Limits[name] = req.DeepCopy()
		}
	}

	return res
}
