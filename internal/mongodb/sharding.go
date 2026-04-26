/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package mongodb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ShardStatus represents the status of a shard
type ShardStatus struct {
	ID    string `json:"_id" bson:"_id"`
	Host  string `json:"host" bson:"host"`
	State int    `json:"state" bson:"state"`
}

// ShardManager는 mongos에 driver로 연결해 sharding 관리 명령을 수행한다.
//
// AddShard 같은 메서드는 mongos 라우터를 통해서만 의미가 있다 — config
// server나 shard pod에 직접 보내면 안 된다. 호출자는 ShardManager를 mongos
// pod에 대한 ConnectFactory로 만들어야 한다.
type ShardManager struct {
	connect ConnectFactory
}

// NewShardManagerWithFactory는 driver client factory를 주입받아 매니저를 만든다.
// factory는 반드시 mongos pod를 대상으로 해야 한다.
func NewShardManagerWithFactory(f ConnectFactory) *ShardManager {
	return &ShardManager{connect: f}
}

// NewShardManager — Deprecated.
func NewShardManager() (*ShardManager, error) {
	return nil, errors.New("NewShardManager는 deprecated. NewShardManagerWithFactory(factory) 사용")
}

// NewShardManagerWithExecutor — Deprecated. Commit 6에서 완전 제거.
func NewShardManagerWithExecutor(exec *Executor) *ShardManager {
	panic("NewShardManagerWithExecutor는 deprecated. NewShardManagerWithFactory 사용")
}

// AddShard adds a shard to the cluster via mongos. Idempotent: 이미 추가된 shard면
// "already a shard" server error를 정상 처리.
func (s *ShardManager) AddShard(ctx context.Context, mongosPod, namespace, shardConnectionString string) error {
	return s.AddShardInContainer(ctx, mongosPod, namespace, "mongos", shardConnectionString, 27017)
}

// AddShardInContainer는 AddShard와 같지만 container/port 인자는 driver 모델에서
// 무시된다. (호환성 유지)
func (s *ShardManager) AddShardInContainer(ctx context.Context, mongosPod, namespace, container, shardConnectionString string, port int) error {
	return s.runAdminCommand(ctx, mongosPod, namespace,
		bson.D{{Key: "addShard", Value: shardConnectionString}}, "addShard")
}

// AddShardWithAuth adds a shard with authentication. adminUser/adminPassword는
// factory가 캡슐화하므로 무시된다 (호환성 유지).
func (s *ShardManager) AddShardWithAuth(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, shardConnectionString string) error {
	return s.AddShard(ctx, mongosPod, namespace, shardConnectionString)
}

// AddShardWithAuthInContainer — 호환성 wrapper.
func (s *ShardManager) AddShardWithAuthInContainer(ctx context.Context, mongosPod, namespace, container, adminUser, adminPassword, shardConnectionString string, port int) error {
	return s.AddShardInContainer(ctx, mongosPod, namespace, container, shardConnectionString, port)
}

// RemoveShard removes a shard from the cluster.
func (s *ShardManager) RemoveShard(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, shardName string) error {
	return s.runAdminCommand(ctx, mongosPod, namespace,
		bson.D{{Key: "removeShard", Value: shardName}}, "removeShard")
}

// ListShards returns the list of shards in the cluster.
func (s *ShardManager) ListShards(ctx context.Context, mongosPod, namespace string) ([]ShardStatus, error) {
	c, err := s.connect(ctx, mongosPod, namespace, true)
	if err != nil {
		return nil, fmt.Errorf("connect for ListShards: %w", err)
	}
	defer disconnectQuiet(c)

	var result struct {
		Shards []ShardStatus `bson:"shards"`
		OK     float64       `bson:"ok"`
	}
	if err := c.Database("admin").RunCommand(ctx, bson.D{{Key: "listShards", Value: 1}}).Decode(&result); err != nil {
		return nil, fmt.Errorf("listShards: %w", err)
	}
	return result.Shards, nil
}

// IsShardAdded checks if a shard is already added to the cluster.
func (s *ShardManager) IsShardAdded(ctx context.Context, mongosPod, namespace, shardName string) (bool, error) {
	shards, err := s.ListShards(ctx, mongosPod, namespace)
	if err != nil {
		return false, err
	}
	for _, shard := range shards {
		if shard.ID == shardName {
			return true, nil
		}
	}
	return false, nil
}

// EnableSharding enables sharding on a database.
func (s *ShardManager) EnableSharding(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, database string) error {
	return s.runAdminCommand(ctx, mongosPod, namespace,
		bson.D{{Key: "enableSharding", Value: database}}, "enableSharding")
}

// ShardCollection shards a collection with the given key.
func (s *ShardManager) ShardCollection(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, collection string, key map[string]interface{}) error {
	return s.runAdminCommand(ctx, mongosPod, namespace, bson.D{
		{Key: "shardCollection", Value: collection},
		{Key: "key", Value: key},
	}, "shardCollection")
}

// runAdminCommand는 mongos에 admin database 명령을 실행하고 idempotent한 server
// error("already a shard", "already enabled" 등)를 정상 처리한다.
func (s *ShardManager) runAdminCommand(ctx context.Context, mongosPod, namespace string, cmd bson.D, op string) error {
	c, err := s.connect(ctx, mongosPod, namespace, true)
	if err != nil {
		return fmt.Errorf("connect for %s: %w", op, err)
	}
	defer disconnectQuiet(c)

	var result bson.M
	err = c.Database("admin").RunCommand(ctx, cmd).Decode(&result)
	if err == nil {
		return nil
	}
	// idempotent server-side errors는 정상 처리.
	var srvErr mongo.ServerError
	if errors.As(err, &srvErr) {
		msg := srvErr.Error()
		if strings.Contains(msg, "already a shard") ||
			strings.Contains(msg, "already exists") ||
			strings.Contains(msg, "already enabled") ||
			strings.Contains(msg, "already sharded") {
			return nil
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// BuildShardConnectionString builds a connection string for adding a shard.
// Format: shardName/host1:port,host2:port,host3:port
func BuildShardConnectionString(shardName, baseName, serviceName, namespace string, members int, port int) string {
	hosts := make([]string, members)
	for i := 0; i < members; i++ {
		podName := fmt.Sprintf("%s-%d", baseName, i)
		hosts[i] = GetPodFQDN(podName, serviceName, namespace, port)
	}
	return fmt.Sprintf("%s/%s", shardName, strings.Join(hosts, ","))
}
