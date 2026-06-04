/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// Package security holds security-hardening helpers (Phase 5.4).
// mtls.go — X.509 inter-node (pod-to-pod) authentication mongod args.
package security

import (
	"fmt"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// DefaultClusterFile is the membership PEM path used for --tlsClusterFile when
// MTLSSpec.ClusterFile is empty. Must match the server PEM mount in
// internal/resources (MongoTLSPEMPath + "/server.pem").
const DefaultClusterFile = "/etc/ssl/mongo-pem/server.pem"

// MongodArgs returns the mongod --clusterAuthMode / --tlsClusterFile flags for
// X.509 inter-node (cluster membership) authentication. Returns nil when the
// spec is nil or disabled (default off → no production impact).
func MongodArgs(spec *mongodbv1alpha1.MTLSSpec) []string {
	if spec == nil || !spec.Enabled {
		return nil
	}
	mode := spec.Mode
	if mode == "" {
		mode = "x509"
	}
	args := []string{fmt.Sprintf("--clusterAuthMode=%s", mode)}
	// keyFile membership does not use a per-member X.509 cluster certificate.
	if mode != "keyFile" {
		clusterFile := spec.ClusterFile
		if clusterFile == "" {
			clusterFile = DefaultClusterFile
		}
		args = append(args, fmt.Sprintf("--tlsClusterFile=%s", clusterFile))
	}
	return args
}
