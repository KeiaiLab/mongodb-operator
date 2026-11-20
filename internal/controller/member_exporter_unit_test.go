/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Member exporter Secret — spec.monitoring.members = Enabled 면 admin 자격으로
// localhost URI Secret 을 만들고, Disabled 면 건드리지 않는지 잠근다.

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	exporterCR     = "exp"
	exporterAdmin  = "exp-admin"
	exporterSecret = exporterCR + "-member-exporter-uri"
)

func exporterSharded(mode mongodbv1alpha1.MemberMonitoring) *mongodbv1alpha1.MongoDBSharded {
	mdbsh := newAdoptedSharded(exporterCR, "9.0.8")
	mdbsh.UID = "exp-uid"
	mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name = exporterAdmin
	mdbsh.Spec.Monitoring = &mongodbv1alpha1.MonitoringSpec{Members: mode}
	return mdbsh
}

func exporterReconciler(t *testing.T, objs ...client.Object) *MongoDBShardedReconciler {
	t.Helper()
	cl := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()
	return &MongoDBShardedReconciler{Client: cl, Scheme: cl.Scheme()}
}

func adminSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: exporterAdmin, Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("p@ss")},
	}
}

func getExporterSecret(t *testing.T, r *MongoDBShardedReconciler) (*corev1.Secret, bool) {
	t.Helper()
	s := &corev1.Secret{}
	err := r.Get(context.Background(), types.NamespacedName{Name: exporterSecret, Namespace: "default"}, s)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(t, err)
	return s, true
}

// TestMemberExporterSecret_Enabled — URI 두 개 · owner = CR.
func TestMemberExporterSecret_Enabled(t *testing.T) {
	mdbsh := exporterSharded(mongodbv1alpha1.MemberMonitoringEnabled)
	r := exporterReconciler(t, mdbsh, adminSecret())

	require.NoError(t, r.reconcileMemberExporterSecret(context.Background(), mdbsh))

	s, ok := getExporterSecret(t, r)
	require.True(t, ok)
	require.Equal(t, "mongodb://admin:p%40ss@localhost:27018/?authSource=admin&directConnection=true", string(s.Data["shard"]))
	require.Equal(t, "mongodb://admin:p%40ss@localhost:27019/?authSource=admin&directConnection=true", string(s.Data["configsvr"]))
	require.Len(t, s.OwnerReferences, 1)
	require.Equal(t, mdbsh.UID, s.OwnerReferences[0].UID)
}

// TestMemberExporterSecret_FollowsPassword — 비밀번호가 바뀌면 URI 도 바뀐다.
func TestMemberExporterSecret_FollowsPassword(t *testing.T) {
	mdbsh := exporterSharded(mongodbv1alpha1.MemberMonitoringEnabled)
	admin := adminSecret()
	r := exporterReconciler(t, mdbsh, admin)
	require.NoError(t, r.reconcileMemberExporterSecret(context.Background(), mdbsh))

	admin.Data["password"] = []byte("new")
	require.NoError(t, r.Update(context.Background(), admin))
	require.NoError(t, r.reconcileMemberExporterSecret(context.Background(), mdbsh))

	s, _ := getExporterSecret(t, r)
	require.Equal(t, "mongodb://admin:new@localhost:27018/?authSource=admin&directConnection=true", string(s.Data["shard"]))
}

// TestMemberExporterSecret_NoAdminFails — admin 자격이 없으면 오류(사이드카가 못 뜬다).
func TestMemberExporterSecret_NoAdminFails(t *testing.T) {
	mdbsh := exporterSharded(mongodbv1alpha1.MemberMonitoringEnabled)
	r := exporterReconciler(t, mdbsh)

	require.Error(t, r.reconcileMemberExporterSecret(context.Background(), mdbsh))
	_, ok := getExporterSecret(t, r)
	require.False(t, ok)

	mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name = ""
	require.Error(t, r.reconcileMemberExporterSecret(context.Background(), mdbsh))
}

// TestMemberExporterSecret_Disabled — 아무것도 만들지 않는다.
func TestMemberExporterSecret_Disabled(t *testing.T) {
	for _, m := range []*mongodbv1alpha1.MonitoringSpec{nil, {Enabled: true}, {Members: mongodbv1alpha1.MemberMonitoringDisabled}} {
		mdbsh := exporterSharded(mongodbv1alpha1.MemberMonitoringDisabled)
		mdbsh.Spec.Monitoring = m
		r := exporterReconciler(t, mdbsh, adminSecret())

		require.NoError(t, r.reconcileMemberExporterSecret(context.Background(), mdbsh))
		_, ok := getExporterSecret(t, r)
		require.False(t, ok)
	}
}
