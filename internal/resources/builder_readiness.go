/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	"fmt"
	"strconv"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// searchGate says whether readiness also waits for the member's search indexes.
type searchGate int

const (
	searchGateOff searchGate = iota
	searchGateOn
)

const (
	// memberStateJS: Ready only as PRIMARY or SECONDARY. hello needs no auth;
	// STARTUP, STARTUP2, RECOVERING, ROLLBACK and an uninitiated member report
	// neither flag.
	memberStateJS = "const h=db.adminCommand({hello:1});" +
		"if(!(h.isWritablePrimary||h.secondary)){quit(1)}"

	// searchQueryableJS: every search index of every user collection on this
	// member must be queryable. Auth failure, a missing password file or a
	// mongot error throws, and mongosh exits non-zero (fail-closed).
	searchQueryableJS = "const pw=require('fs').readFileSync('/etc/mongodb-admin/password','utf8').trim();" +
		"const a=db.getSiblingDB('admin');a.auth('admin',pw);" +
		"for(const d of a.adminCommand({listDatabases:1,nameOnly:true}).databases){" +
		"if(['admin','config','local'].includes(d.name)){continue}" +
		"const s=db.getSiblingDB(d.name);" +
		"for(const c of s.getCollectionInfos({type:'collection'})){" +
		"if(c.name.startsWith('system.')){continue}" +
		"if(s.getCollection(c.name).aggregate([{$listSearchIndexes:{}}]).toArray().some(i=>!i.queryable)){quit(1)}" +
		"}}"
)

// mongodReadiness is the mongod readiness exec command for a shard or config
// server member. Ping (or empty) keeps the ping probe byte for byte.
func mongodReadiness(port int, mode mongodbv1alpha1.ReadinessMode, gate searchGate) []string {
	if mode != mongodbv1alpha1.ReadinessMemberState {
		return mongoshPing(port)
	}

	js := memberStateJS
	if gate == searchGateOn {
		js += searchQueryableJS
	}
	return []string{"mongosh", "--norc", "--quiet", "--port", strconv.Itoa(port), "--eval", js}
}

// shardSearchGate is on for the mongot router shard, the shard the mongot
// Service selects, when its pods carry the mongot sidecar.
func shardSearchGate(mdbsh *mongodbv1alpha1.MongoDBSharded, shardIndex int32) searchGate {
	if mdbsh.Annotations[MongotSidecarImageAnnotation] == "" {
		return searchGateOff
	}
	if MongotRouterShard(mdbsh) != fmt.Sprintf("shard-%d", shardIndex) {
		return searchGateOff
	}
	return searchGateOn
}
