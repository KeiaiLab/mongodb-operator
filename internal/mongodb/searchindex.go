/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// searchindex.go — MongoDBSearchIndex 컨트롤러용 search index 관리(mongo-driver v2
// SearchIndexView). search index 명령은 *source mongod* 에 실행하며 mongod 가 mongot
// (sidecar)에 forward 한다 — operator 는 mongot 에 직접 연결하지 않는다. ConnectFactory 를
// 재사용해 admin 인증으로 연결한다(replicaset.go 패턴).
package mongodb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// SearchIndexInfo — $listSearchIndexes 결과의 1개 인덱스 상태.
type SearchIndexInfo struct {
	ID        string `bson:"id"`
	Name      string `bson:"name"`
	Status    string `bson:"status"` // PENDING|BUILDING|READY|FAILED|STALE|DOES_NOT_EXIST
	Queryable bool   `bson:"queryable"`
}

// SearchIndexManager — source mongod 에 search index CRUD. ConnectFactory 재사용.
type SearchIndexManager struct {
	connect ConnectFactory
}

// NewSearchIndexManagerWithFactory — driver client factory 주입(테스트 시 fake 교체).
func NewSearchIndexManagerWithFactory(f ConnectFactory) *SearchIndexManager {
	return &SearchIndexManager{connect: f}
}

// List — 지정 name 의 search index 상태 조회($listSearchIndexes). 없으면 빈 slice.
func (m *SearchIndexManager) List(ctx context.Context, podName, namespace, db, coll, name string) ([]SearchIndexInfo, error) {
	c, err := m.connect(ctx, podName, namespace, false)
	if err != nil {
		return nil, fmt.Errorf("connect for search index list: %w", err)
	}
	defer disconnectQuiet(c)

	cursor, err := c.Database(db).Collection(coll).SearchIndexes().List(ctx, options.SearchIndexes().SetName(name))
	if err != nil {
		return nil, fmt.Errorf("listSearchIndexes %s.%s/%s: %w", db, coll, name, err)
	}
	var out []SearchIndexInfo
	if err := cursor.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode search indexes: %w", err)
	}
	return out, nil
}

// Create — search index 생성(createSearchIndexes). indexType = "search"|"vectorSearch".
// def = 파싱된 인덱스 정의(bson). 반환 = mongot 할당 index name.
func (m *SearchIndexManager) Create(ctx context.Context, podName, namespace, db, coll, name, indexType string, def bson.M) (string, error) {
	c, err := m.connect(ctx, podName, namespace, false)
	if err != nil {
		return "", fmt.Errorf("connect for search index create: %w", err)
	}
	defer disconnectQuiet(c)

	model := mongo.SearchIndexModel{
		Definition: def,
		Options:    options.SearchIndexes().SetName(name).SetType(indexType),
	}
	created, err := c.Database(db).Collection(coll).SearchIndexes().CreateOne(ctx, model)
	if err != nil {
		return "", fmt.Errorf("createSearchIndex %s.%s/%s: %w", db, coll, name, err)
	}
	return created, nil
}

// Update — 기존 search index 정의 갱신(updateSearchIndex). vectorSearch 는 일부 필드만 변경 가능.
func (m *SearchIndexManager) Update(ctx context.Context, podName, namespace, db, coll, name string, def bson.M) error {
	c, err := m.connect(ctx, podName, namespace, false)
	if err != nil {
		return fmt.Errorf("connect for search index update: %w", err)
	}
	defer disconnectQuiet(c)

	if err := c.Database(db).Collection(coll).SearchIndexes().UpdateOne(ctx, name, def); err != nil {
		return fmt.Errorf("updateSearchIndex %s.%s/%s: %w", db, coll, name, err)
	}
	return nil
}

// Drop — search index 삭제(dropSearchIndex). 멱등: 인덱스/namespace 부재는 성공 처리
// (finalizer cleanup 이 CR 을 wedge 하지 않게).
func (m *SearchIndexManager) Drop(ctx context.Context, podName, namespace, db, coll, name string) error {
	c, err := m.connect(ctx, podName, namespace, false)
	if err != nil {
		return fmt.Errorf("connect for search index drop: %w", err)
	}
	defer disconnectQuiet(c)

	if err := c.Database(db).Collection(coll).SearchIndexes().DropOne(ctx, name); err != nil {
		if isIndexNotFoundErr(err) {
			return nil // 멱등: 이미 없음
		}
		return fmt.Errorf("dropSearchIndex %s.%s/%s: %w", db, coll, name, err)
	}
	return nil
}

// mongot index status 상수(mongo-driver $listSearchIndexes status 필드).
const (
	searchIndexStatusReady      = "READY"
	searchIndexStatusPending    = "PENDING"
	searchIndexStatusBuilding   = "BUILDING"
	searchIndexStatusStale      = "STALE"
	searchIndexStatusFailed     = "FAILED"
	searchIndexStatusDoesNotEx  = "DOES_NOT_EXIST"
	mongoErrIndexNotFound       = 27 // IndexNotFound
	mongoErrNamespaceNotFound   = 26 // NamespaceNotFound
	mongoErrSearchIndexNotFound = 220
)

// SearchIndexPhase — CRD Status.Phase 값(mongodbsearchindex_types.go enum 정합).
type SearchIndexPhase string

const (
	SearchIndexPhasePending  SearchIndexPhase = "Pending"
	SearchIndexPhaseBuilding SearchIndexPhase = "Building"
	SearchIndexPhaseReady    SearchIndexPhase = "Ready"
	SearchIndexPhaseFailed   SearchIndexPhase = "Failed"
)

// ClassifyMongotStatus — mongot index status + queryable → CRD Phase 매핑(순수 함수).
// READY && queryable → Ready / PENDING|BUILDING|STALE → Building / FAILED → Failed /
// DOES_NOT_EXIST 또는 미지 status → Pending(재생성 트리거).
func ClassifyMongotStatus(status string, queryable bool) SearchIndexPhase {
	switch strings.ToUpper(status) {
	case searchIndexStatusReady:
		if queryable {
			return SearchIndexPhaseReady
		}
		return SearchIndexPhaseBuilding // READY 인데 미queryable = 아직 준비 중
	case searchIndexStatusPending, searchIndexStatusBuilding, searchIndexStatusStale:
		return SearchIndexPhaseBuilding
	case searchIndexStatusFailed:
		return SearchIndexPhaseFailed
	case searchIndexStatusDoesNotEx:
		return SearchIndexPhasePending
	default:
		// 미지 status(DOES_NOT_EXIST 포함되나 명시) → 재생성 트리거.
		return SearchIndexPhasePending
	}
}

// isIndexNotFoundErr — dropSearchIndex 멱등 처리용. IndexNotFound(27) / NamespaceNotFound(26) /
// SearchIndexNotFound(220) 또는 메시지 fallback.
func isIndexNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	var srvErr mongo.ServerError
	if errors.As(err, &srvErr) {
		if srvErr.HasErrorCode(mongoErrIndexNotFound) ||
			srvErr.HasErrorCode(mongoErrNamespaceNotFound) ||
			srvErr.HasErrorCode(mongoErrSearchIndexNotFound) {
			return true
		}
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "index not found") ||
		strings.Contains(s, "ns not found") ||
		strings.Contains(s, "does not exist")
}
