/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package controller

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/resources"
)

// errMemberExporterNoAdmin: the member exporters authenticate as admin; without
// the admin Secret their URI Secret cannot exist and the sidecars cannot start.
var errMemberExporterNoAdmin = errors.New("monitoring.members=Enabled needs auth.adminCredentialsSecretRef")

// reconcileMemberExporterSecret writes the localhost URI Secret of the shard
// and cfg exporter sidecars. Disabled: nothing is created or removed.
func (r *MongoDBShardedReconciler) reconcileMemberExporterSecret(ctx context.Context, mdbsh *mongodbv1alpha1.MongoDBSharded) error {
	if !resources.MemberExportersEnabled(mdbsh) {
		return nil
	}
	if mdbsh.Spec.Auth.AdminCredentialsSecretRef.Name == "" {
		return errMemberExporterNoAdmin
	}

	password, err := r.getAdminPassword(ctx, mdbsh)
	if err != nil {
		return err
	}

	desired := resources.BuildMemberExporterSecret(mdbsh, password)
	live := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, live, func() error {
		live.Labels = desired.Labels
		live.Type = desired.Type
		live.Data = desired.Data
		return controllerutil.SetControllerReference(mdbsh, live, r.Scheme)
	})
	return err
}
