/*
Copyright 2026 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Finalizer string constants — cross-repo SSoT (B-P0-1).
//
// 외부 사용자 (kubectl jsonpath / ArgoCD finalizer cleanup / Argo Events) 가
// 의존하는 *공개 wire contract*. 변경 시 SemVer major bump + 1 release migration
// window (controller 가 old + new 둘 다 인식) 의무.
//
// 명명 규약: `<group>.keiailab.com/<kind-name>-finalizer`. 본 SSoT 시점에는
// *기존 production CR 호환* 위해 wire-level string 보존 (v1.4 chain). v1.5+ 에서
// 명명 통일 (group: keiailab.com → keiailab.io / kind-suffix 추가) 별 ADR + RFC.
package v1beta1

const (
	// FinalizerMongoDB — MongoDB CR 삭제 시 controller cleanup 보장.
	// keiailab-platform-data v1.4.5+ production CR 가 의존.
	FinalizerMongoDB = "mongodb.keiailab.com/finalizer"

	// FinalizerMongoDBSharded — MongoDBSharded CR 삭제 시 cleanup.
	// configServer + shard + mongos statefulset cleanup 의무.
	FinalizerMongoDBSharded = "mongodbsharded.keiailab.com/finalizer"

	// FinalizerMongoDBBackup — MongoDBBackup CR 삭제 시 cleanup.
	// backup job + S3 storage credential secret cleanup.
	FinalizerMongoDBBackup = "mongodbbackup.keiailab.com/finalizer"
)
