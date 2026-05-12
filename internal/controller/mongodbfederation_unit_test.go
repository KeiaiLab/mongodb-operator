/*
Copyright 2026 Keiailab.
*/

package controller

import (
	"testing"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestComputeFederationPhase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []mongodbv1alpha1.FederationRegionStatus
		want string
	}{
		{"empty", nil, federationPhaseBootstrapping},
		{"all synced", []mongodbv1alpha1.FederationRegionStatus{{Name: "a", Phase: "Synced"}, {Name: "b", Phase: "Synced"}}, federationPhaseSynced},
		{"one failed", []mongodbv1alpha1.FederationRegionStatus{{Name: "a", Phase: "Synced"}, {Name: "b", Phase: "Failed"}}, federationPhaseDegraded},
		{"partial pending", []mongodbv1alpha1.FederationRegionStatus{{Name: "a", Phase: "Synced"}, {Name: "b", Phase: "Pending"}}, federationPhaseBootstrapping},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fed := &mongodbv1alpha1.MongoDBFederation{
				Status: mongodbv1alpha1.MongoDBFederationStatus{RegionStatuses: tc.in},
			}
			if got := computeFederationPhase(fed); got != tc.want {
				t.Errorf("computeFederationPhase: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestHasRegionStatus(t *testing.T) {
	t.Parallel()
	statuses := []mongodbv1alpha1.FederationRegionStatus{
		{Name: "us-west", Phase: "Synced"},
		{Name: "eu-central", Phase: "Pending"},
	}
	if !hasRegionStatus(statuses, "us-west") {
		t.Errorf("us-west must be found")
	}
	if hasRegionStatus(statuses, "ap-south") {
		t.Errorf("ap-south must not be found")
	}
}
