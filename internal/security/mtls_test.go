/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package security

import (
	"reflect"
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestNextClusterAuthMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name             string
		current, desired string
		want             string
	}{
		{"forward 1: keyFile→x509 한 단계", "keyFile", "x509", "sendKeyFile"},
		{"forward 2: sendKeyFile→x509", "sendKeyFile", "x509", "sendX509"},
		{"forward 3: sendX509→x509 도달", "sendX509", "x509", "x509"},
		{"이미 도달: x509→x509", "x509", "x509", "x509"},
		{"reverse 1: x509→keyFile 한 단계", "x509", "keyFile", "sendX509"},
		{"동일: keyFile→keyFile", "keyFile", "keyFile", "keyFile"},
		{"off-path current → desired 점프", "", "x509", "x509"},
		{"off-path desired → desired 점프", "keyFile", "weird", "weird"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NextClusterAuthMode(tc.current, tc.desired); got != tc.want {
				t.Errorf("NextClusterAuthMode(%q, %q) = %q, want %q", tc.current, tc.desired, got, tc.want)
			}
		})
	}
}

func TestMembershipSubject(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		spec    *mongodbv1alpha1.MTLSSpec
		cluster string
		want    map[string]any
	}{
		{"nil spec → nil", nil, "keiailab-mongo", nil},
		{"disabled → nil", &mongodbv1alpha1.MTLSSpec{Enabled: false}, "keiailab-mongo", nil},
		{
			"enabled → shared O + per-cluster OU",
			&mongodbv1alpha1.MTLSSpec{Enabled: true},
			"keiailab-mongo",
			map[string]any{
				"organizations":       []any{MembershipOrg},
				"organizationalUnits": []any{"keiailab-mongo"},
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MembershipSubject(tc.spec, tc.cluster)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MembershipSubject(%+v, %q) = %v, want %v", tc.spec, tc.cluster, got, tc.want)
			}
		})
	}
}

func TestMongodArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		spec *mongodbv1alpha1.MTLSSpec
		want []string
	}{
		{"nil spec → nil", nil, nil},
		{"disabled → nil (default off)", &mongodbv1alpha1.MTLSSpec{Enabled: false, Mode: "x509"}, nil},
		{
			"enabled default mode → x509 + default clusterFile",
			&mongodbv1alpha1.MTLSSpec{Enabled: true},
			[]string{"--clusterAuthMode=x509", "--tlsClusterFile=/etc/ssl/mongo-pem/server.pem"},
		},
		{
			"enabled x509 custom clusterFile",
			&mongodbv1alpha1.MTLSSpec{Enabled: true, Mode: "x509", ClusterFile: "/custom/cluster.pem"},
			[]string{"--clusterAuthMode=x509", "--tlsClusterFile=/custom/cluster.pem"},
		},
		{
			"sendX509 rolling transition → mode + clusterFile",
			&mongodbv1alpha1.MTLSSpec{Enabled: true, Mode: "sendX509"},
			[]string{"--clusterAuthMode=sendX509", "--tlsClusterFile=/etc/ssl/mongo-pem/server.pem"},
		},
		{
			"keyFile mode → no clusterFile (no cert needed)",
			&mongodbv1alpha1.MTLSSpec{Enabled: true, Mode: "keyFile"},
			[]string{"--clusterAuthMode=keyFile"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MongodArgs(tc.spec)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MongodArgs(%+v) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}
