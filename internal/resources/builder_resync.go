/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/assets"
)

const (
	resyncInitName  = "resync-marker"
	resyncVolume    = "resync-marker"
	resyncMountPath = "/resync"
	resyncCMSuffix  = "-resync"
)

// ResyncConfigMapName is the optional marker ConfigMap: key = pod name,
// value = marker. A converger writes it; the operator only mounts it.
func ResyncConfigMapName(instanceName string) string { return instanceName + resyncCMSuffix }

func buildResyncScript() string {
	out, err := assets.RenderResyncMarker()
	if err != nil {
		panic(fmt.Sprintf("render resync marker script: %v", err))
	}
	return out
}

// applyResyncMarker adds the marker init container (right after copy-keyfile,
// before anything else touches the data volume) and the optional marker
// ConfigMap volume. Disabled (or empty) leaves the set unchanged.
func applyResyncMarker(sts *appsv1.StatefulSet, mode mongodbv1alpha1.ResyncMode, instanceName, dbpath string) {
	if mode != mongodbv1alpha1.ResyncMarker {
		return
	}

	spec := &sts.Spec.Template.Spec
	init := corev1.Container{
		Name:    resyncInitName,
		Image:   keyfileInitImage,
		Command: []string{"sh", "-c", buildResyncScript()},
		Env: []corev1.EnvVar{
			{Name: "DBPATH", Value: dbpath},
			{Name: "RESYNC_DIR", Value: resyncMountPath},
			{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
			}},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: mongodDataVolume, MountPath: dbpath},
			{Name: resyncVolume, MountPath: resyncMountPath, ReadOnly: true},
		},
		SecurityContext: buildKeyfileInitContainerSecurityContext(),
	}
	spec.InitContainers = append(spec.InitContainers[:1], append([]corev1.Container{init}, spec.InitContainers[1:]...)...)

	spec.Volumes = append(spec.Volumes, corev1.Volume{
		Name: resyncVolume,
		VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: ResyncConfigMapName(instanceName)},
			Optional:             ptr.To(true),
		}},
	})
}
