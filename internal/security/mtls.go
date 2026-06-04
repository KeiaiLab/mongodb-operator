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

// ClusterAuthOrder is the safe rolling transition path for mongod
// --clusterAuthMode. mongod members can only differ by one adjacent step during
// a transition (skipping a step makes members reject each other), so the
// operator advances one position per reconcile.
var ClusterAuthOrder = []string{"keyFile", "sendKeyFile", "sendX509", "x509"}

// NextClusterAuthMode returns the next clusterAuthMode on the safe rolling path
// from current toward desired — exactly one adjacent step. Returns desired when
// already there, or when either value is off-path (no safe sequence to walk).
func NextClusterAuthMode(current, desired string) string {
	ci, di := indexOf(ClusterAuthOrder, current), indexOf(ClusterAuthOrder, desired)
	if ci < 0 || di < 0 {
		return desired // off-path → no safe sequence, caller jumps directly
	}
	switch {
	case ci < di:
		return ClusterAuthOrder[ci+1]
	case ci > di:
		return ClusterAuthOrder[ci-1]
	default:
		return desired
	}
}

func indexOf(xs []string, v string) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
}

// MembershipOrg is the X.509 organization shared by every cluster member cert.
// mongod treats certs sharing O+OU as members of the same cluster.
const MembershipOrg = "keiailab-mongodb-cluster"

// MembershipSubject returns the cert-manager Certificate subject for X.509
// cluster membership: O=MembershipOrg, OU=<clusterName>. All member certs of one
// cluster must share these so mongod recognises them as the same cluster.
// Returns nil when mTLS is disabled (server cert keeps its default subject).
func MembershipSubject(spec *mongodbv1alpha1.MTLSSpec, clusterName string) map[string]any {
	if spec == nil || !spec.Enabled {
		return nil
	}
	return map[string]any{
		"organizations":       []any{MembershipOrg},
		"organizationalUnits": []any{clusterName},
	}
}

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
