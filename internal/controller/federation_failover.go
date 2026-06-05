/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// federation_failover.go — Phase 5.2 multi-region federation: primary 선출 *결정*.
//
// 여러 region 의 상태 + priority 로 active primary region 을 결정하는 *순수 함수*.
// 실 promotion (remote cluster 의 rs.reconfig priority 변경)은 cross-cluster I/O 로
// 후속 (federation_propagation.go). 본 함수는 결정만 — computeFederationPhase sister.
package controller

import (
	"sort"
	"strconv"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// FederationPrimaryDecision is the advisory outcome of primary region selection.
type FederationPrimaryDecision struct {
	// PrimaryRegion is the chosen active primary; empty when none is eligible.
	PrimaryRegion string
	// EligibleRegions lists all Synced regions (name-sorted), including
	// secondary-only (priority 0) ones — observability into the candidate set.
	EligibleRegions []string
	// Reason explains the selection (or why no primary could be chosen).
	Reason string
}

// parseRegionPriority parses a FederationRegion.Priority string, defaulting to
// 1.0 (the CRD default) when empty or unparseable so a malformed value never
// silently drops a region from primary eligibility.
func parseRegionPriority(p string) float64 {
	if p == "" {
		return 1.0
	}
	v, err := strconv.ParseFloat(p, 64)
	if err != nil {
		return 1.0
	}
	return v
}

// DecideFederationPrimary selects the active primary region: the highest-priority
// region that is currently Synced. Priority 0 regions are secondary-only (DR /
// analytics) and never become primary. Ties break by region name (ascending).
// When no Synced region with priority > 0 exists, PrimaryRegion is empty with a
// descriptive Reason. Pure function — no side effects (caller persists the result).
func DecideFederationPrimary(regions []mongodbv1alpha1.FederationRegion, statuses []mongodbv1alpha1.FederationRegionStatus) FederationPrimaryDecision {
	phaseByName := make(map[string]string, len(statuses))
	for _, s := range statuses {
		phaseByName[s.Name] = s.Phase
	}

	type candidate struct {
		name     string
		priority float64
	}
	var synced []string
	var primaryCandidates []candidate
	for _, r := range regions {
		if phaseByName[r.Name] != federationPhaseSynced {
			continue
		}
		synced = append(synced, r.Name)
		if prio := parseRegionPriority(r.Priority); prio > 0 {
			primaryCandidates = append(primaryCandidates, candidate{r.Name, prio})
		}
	}
	sort.Strings(synced)

	if len(synced) == 0 {
		return FederationPrimaryDecision{EligibleRegions: synced, Reason: "Synced region 없음 — primary 선출 불가"}
	}
	if len(primaryCandidates) == 0 {
		return FederationPrimaryDecision{EligibleRegions: synced, Reason: "모든 Synced region 이 secondary-only (priority 0) — primary 후보 없음"}
	}

	// 최고 priority 우선, 동점 시 name 사전순.
	sort.Slice(primaryCandidates, func(i, j int) bool {
		if primaryCandidates[i].priority != primaryCandidates[j].priority {
			return primaryCandidates[i].priority > primaryCandidates[j].priority
		}
		return primaryCandidates[i].name < primaryCandidates[j].name
	})
	return FederationPrimaryDecision{
		PrimaryRegion:   primaryCandidates[0].name,
		EligibleRegions: synced,
		Reason:          "최고 priority Synced region 선출",
	}
}
