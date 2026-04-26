/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mongodb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ShardStatus represents the status of a shard
type ShardStatus struct {
	ID    string `json:"_id"`
	Host  string `json:"host"`
	State int    `json:"state"`
}

// ShardingStatus represents the sharding status of the cluster
type ShardingStatus struct {
	Shards []ShardStatus `json:"shards"`
	OK     int           `json:"ok"`
}

// ShardManager manages MongoDB sharding operations
type ShardManager struct {
	executor *Executor
}

// NewShardManager creates a new shard manager
func NewShardManager() (*ShardManager, error) {
	exec, err := NewExecutor()
	if err != nil {
		return nil, err
	}
	return &ShardManager{executor: exec}, nil
}

// NewShardManagerWithExecutor creates a new shard manager with provided executor
func NewShardManagerWithExecutor(exec *Executor) *ShardManager {
	return &ShardManager{executor: exec}
}

// AddShard adds a shard to the cluster via mongos
func (s *ShardManager) AddShard(ctx context.Context, mongosPod, namespace, shardConnectionString string) error {
	return s.AddShardInContainer(ctx, mongosPod, namespace, "mongodb", shardConnectionString, 27017)
}

// AddShardInContainer adds a shard to the cluster via mongos in a specified container
func (s *ShardManager) AddShardInContainer(ctx context.Context, mongosPod, namespace, container, shardConnectionString string, port int) error {
	command := fmt.Sprintf("sh.addShard(%s)", jsString(shardConnectionString))
	result, err := s.executor.ExecuteMongoshInContainer(ctx, mongosPod, namespace, container, command, port)
	if err != nil {
		return fmt.Errorf("failed to add shard: %w", err)
	}

	// Check if shard already exists (not an error)
	if strings.Contains(result.Stderr, "already exists") || strings.Contains(result.Stdout, "already exists") {
		return nil
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("sh.addShard failed: stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	}

	return nil
}

// AddShardWithAuth adds a shard to the cluster via mongos with authentication
func (s *ShardManager) AddShardWithAuth(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, shardConnectionString string) error {
	return s.AddShardWithAuthInContainer(ctx, mongosPod, namespace, "mongodb", adminUser, adminPassword, shardConnectionString, 27017)
}

// AddShardWithAuthInContainer adds a shard with auth in a specified container
func (s *ShardManager) AddShardWithAuthInContainer(ctx context.Context, mongosPod, namespace, container, adminUser, adminPassword, shardConnectionString string, port int) error {
	command := fmt.Sprintf("sh.addShard(%s)", jsString(shardConnectionString))
	result, err := s.executor.ExecuteMongoshWithAuthInContainer(ctx, mongosPod, namespace, container, adminUser, adminPassword, "admin", command, port)
	if err != nil {
		return fmt.Errorf("failed to add shard: %w", err)
	}

	// Check if shard already exists (not an error)
	if strings.Contains(result.Stderr, "already exists") || strings.Contains(result.Stdout, "already exists") {
		return nil
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("sh.addShard failed: stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	}

	return nil
}

// RemoveShard removes a shard from the cluster
func (s *ShardManager) RemoveShard(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, shardName string) error {
	command := fmt.Sprintf("db.adminCommand({ removeShard: %s })", jsString(shardName))
	result, err := s.executor.ExecuteMongoshWithAuth(ctx, mongosPod, namespace, adminUser, adminPassword, "admin", command)
	if err != nil {
		return fmt.Errorf("failed to remove shard: %w", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("removeShard failed: %s", result.Stderr)
	}

	return nil
}

// ListShards returns the list of shards in the cluster
func (s *ShardManager) ListShards(ctx context.Context, mongosPod, namespace string) ([]ShardStatus, error) {
	result, err := s.executor.ExecuteMongoshJSON(ctx, mongosPod, namespace, "db.adminCommand({ listShards: 1 })")
	if err != nil {
		return nil, fmt.Errorf("failed to list shards: %w", err)
	}

	if result.ExitCode != 0 {
		return nil, fmt.Errorf("listShards failed: %s", result.Stderr)
	}

	var status ShardingStatus
	if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
		return nil, fmt.Errorf("failed to parse sharding status: %w", err)
	}

	return status.Shards, nil
}

// ListShardsWithAuth returns the list of shards with authentication
func (s *ShardManager) ListShardsWithAuth(ctx context.Context, mongosPod, namespace, adminUser, adminPassword string) ([]ShardStatus, error) {
	command := "JSON.stringify(db.adminCommand({ listShards: 1 }))"
	result, err := s.executor.ExecuteMongoshWithAuth(ctx, mongosPod, namespace, adminUser, adminPassword, "admin", command)
	if err != nil {
		return nil, fmt.Errorf("failed to list shards: %w", err)
	}

	if result.ExitCode != 0 {
		return nil, fmt.Errorf("listShards failed: %s", result.Stderr)
	}

	var status ShardingStatus
	if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
		return nil, fmt.Errorf("failed to parse sharding status: %w", err)
	}

	return status.Shards, nil
}

// IsShardAdded checks if a shard is already added to the cluster
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

// IsShardAddedWithAuth checks if a shard is already added to the cluster (with auth)
func (s *ShardManager) IsShardAddedWithAuth(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, shardName string) (bool, error) {
	shards, err := s.ListShardsWithAuth(ctx, mongosPod, namespace, adminUser, adminPassword)
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

// GetShardingStatus returns the full sharding status
func (s *ShardManager) GetShardingStatus(ctx context.Context, mongosPod, namespace string) (string, error) {
	result, err := s.executor.ExecuteMongosh(ctx, mongosPod, namespace, "sh.status()")
	if err != nil {
		return "", fmt.Errorf("failed to get sharding status: %w", err)
	}

	return result.Stdout, nil
}

// EnableSharding enables sharding on a database
func (s *ShardManager) EnableSharding(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, database string) error {
	command := fmt.Sprintf("sh.enableSharding(%s)", jsString(database))
	result, err := s.executor.ExecuteMongoshWithAuth(ctx, mongosPod, namespace, adminUser, adminPassword, "admin", command)
	if err != nil {
		return fmt.Errorf("failed to enable sharding: %w", err)
	}

	// Check if already enabled
	if strings.Contains(result.Stderr, "already enabled") || strings.Contains(result.Stdout, "already enabled") {
		return nil
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("enableSharding failed: %s", result.Stderr)
	}

	return nil
}

// ShardCollection shards a collection
func (s *ShardManager) ShardCollection(ctx context.Context, mongosPod, namespace, adminUser, adminPassword, collection string, key map[string]interface{}) error {
	keyJSON, err := json.Marshal(key)
	if err != nil {
		return fmt.Errorf("failed to marshal shard key: %w", err)
	}

	command := fmt.Sprintf("sh.shardCollection(%s, %s)", jsString(collection), string(keyJSON))
	result, err := s.executor.ExecuteMongoshWithAuth(ctx, mongosPod, namespace, adminUser, adminPassword, "admin", command)
	if err != nil {
		return fmt.Errorf("failed to shard collection: %w", err)
	}

	// Check if already sharded
	if strings.Contains(result.Stderr, "already sharded") || strings.Contains(result.Stdout, "already sharded") {
		return nil
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("shardCollection failed: %s", result.Stderr)
	}

	return nil
}

// BuildShardConnectionString builds a connection string for adding a shard
// Format: shardName/host1:port,host2:port,host3:port
func BuildShardConnectionString(shardName, baseName, serviceName, namespace string, members int, port int) string {
	hosts := make([]string, members)
	for i := 0; i < members; i++ {
		podName := fmt.Sprintf("%s-%d", baseName, i)
		hosts[i] = GetPodFQDN(podName, serviceName, namespace, port)
	}
	return fmt.Sprintf("%s/%s", shardName, strings.Join(hosts, ","))
}
