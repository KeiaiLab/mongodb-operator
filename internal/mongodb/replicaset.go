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

// ReplicaSetConfig represents a MongoDB replica set configuration
type ReplicaSetConfig struct {
	ID      string             `json:"_id"`
	Members []ReplicaSetMember `json:"members"`
	Version int                `json:"version,omitempty"`
}

// ReplicaSetMember represents a member in a replica set
type ReplicaSetMember struct {
	ID          int     `json:"_id"`
	Host        string  `json:"host"`
	Priority    float64 `json:"priority,omitempty"`
	Votes       int     `json:"votes,omitempty"`
	ArbiterOnly bool    `json:"arbiterOnly,omitempty"`
	Hidden      bool    `json:"hidden,omitempty"`
}

// ReplicaSetStatus represents the status of a replica set
type ReplicaSetStatus struct {
	Set     string                   `json:"set"`
	MyState int                      `json:"myState"`
	Members []ReplicaSetMemberStatus `json:"members"`
	OK      int                      `json:"ok"`
}

// ReplicaSetMemberStatus represents the status of a replica set member
type ReplicaSetMemberStatus struct {
	ID       int    `json:"_id"`
	Name     string `json:"name"`
	Health   int    `json:"health"`
	State    int    `json:"state"`
	StateStr string `json:"stateStr"`
	Uptime   int64  `json:"uptime"`
	Self     bool   `json:"self,omitempty"`
}

// ReplicaSetManager manages MongoDB replica set operations
type ReplicaSetManager struct {
	executor *Executor
	port     int
}

// NewReplicaSetManager creates a new replica set manager with default port 27017
func NewReplicaSetManager() (*ReplicaSetManager, error) {
	return NewReplicaSetManagerWithPort(27017)
}

// NewReplicaSetManagerWithPort creates a new replica set manager with specified port
func NewReplicaSetManagerWithPort(port int) (*ReplicaSetManager, error) {
	exec, err := NewExecutor()
	if err != nil {
		return nil, err
	}
	return &ReplicaSetManager{executor: exec, port: port}, nil
}

// NewReplicaSetManagerWithExecutor creates a new replica set manager with provided executor
func NewReplicaSetManagerWithExecutor(exec *Executor) *ReplicaSetManager {
	return &ReplicaSetManager{executor: exec, port: 27017}
}

// NewReplicaSetManagerWithExecutorAndPort creates a new replica set manager with provided executor and port
func NewReplicaSetManagerWithExecutorAndPort(exec *Executor, port int) *ReplicaSetManager {
	return &ReplicaSetManager{executor: exec, port: port}
}

// IsInitialized checks if the replica set is already initialized
func (r *ReplicaSetManager) IsInitialized(ctx context.Context, podName, namespace string) (bool, error) {
	result, err := r.executor.ExecuteMongoshWithPort(ctx, podName, namespace, "rs.status().ok", r.port)
	if err != nil {
		return false, nil // Not initialized or error
	}

	// If we get "1" back, it's initialized
	if strings.TrimSpace(result.Stdout) == "1" {
		return true, nil
	}

	// Check stderr for "no replset config" which means not initialized
	if strings.Contains(result.Stderr, "no replset config") ||
		strings.Contains(result.Stderr, "NotYetInitialized") {
		return false, nil
	}

	return false, nil
}

// Initiate initializes a new replica set
func (r *ReplicaSetManager) Initiate(ctx context.Context, podName, namespace string, config ReplicaSetConfig) error {
	// Check if already initialized
	initialized, err := r.IsInitialized(ctx, podName, namespace)
	if err != nil {
		return fmt.Errorf("failed to check initialization status: %w", err)
	}
	if initialized {
		return nil // Already initialized
	}

	// Build the rs.initiate() command
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	command := fmt.Sprintf("rs.initiate(%s)", string(configJSON))
	result, err := r.executor.ExecuteMongoshWithPort(ctx, podName, namespace, command, r.port)
	if err != nil {
		return fmt.Errorf("failed to initiate replica set: %w", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("rs.initiate failed: %s", result.Stderr)
	}

	return nil
}

// GetStatus returns the current replica set status
func (r *ReplicaSetManager) GetStatus(ctx context.Context, podName, namespace string) (*ReplicaSetStatus, error) {
	result, err := r.executor.ExecuteMongoshJSONWithPort(ctx, podName, namespace, "rs.status()", r.port)
	if err != nil {
		return nil, fmt.Errorf("failed to get replica set status: %w", err)
	}

	if result.ExitCode != 0 {
		return nil, fmt.Errorf("rs.status() failed: %s", result.Stderr)
	}

	var status ReplicaSetStatus
	if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
		return nil, fmt.Errorf("failed to parse replica set status: %w", err)
	}

	return &status, nil
}

// GetPrimaryPod returns the name of the primary pod
func (r *ReplicaSetManager) GetPrimaryPod(ctx context.Context, podName, namespace string) (string, error) {
	status, err := r.GetStatus(ctx, podName, namespace)
	if err != nil {
		return "", err
	}

	for _, member := range status.Members {
		if member.StateStr == "PRIMARY" {
			// Extract pod name from host (e.g., "my-mongodb-0.my-mongodb-headless.ns.svc.cluster.local:27017")
			parts := strings.Split(member.Name, ".")
			if len(parts) > 0 {
				return parts[0], nil
			}
		}
	}

	return "", fmt.Errorf("no primary found")
}

// HasPrimary checks if the replica set has an elected primary
func (r *ReplicaSetManager) HasPrimary(ctx context.Context, podName, namespace string) (bool, error) {
	status, err := r.GetStatus(ctx, podName, namespace)
	if err != nil {
		return false, err
	}

	for _, member := range status.Members {
		if member.StateStr == "PRIMARY" && member.Health == 1 {
			return true, nil
		}
	}

	return false, nil
}

// WaitForPrimary waits until a primary is elected (using context for timeout)
func (r *ReplicaSetManager) WaitForPrimary(ctx context.Context, podName, namespace string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			hasPrimary, err := r.HasPrimary(ctx, podName, namespace)
			if err == nil && hasPrimary {
				return nil
			}
			// Continue waiting
		}
	}
}

// AddMember adds a new member to the replica set
func (r *ReplicaSetManager) AddMember(ctx context.Context, podName, namespace, newHost string, arbiterOnly bool) error {
	var command string
	if arbiterOnly {
		command = fmt.Sprintf("rs.addArb(%s)", jsString(newHost))
	} else {
		command = fmt.Sprintf("rs.add(%s)", jsString(newHost))
	}

	result, err := r.executor.ExecuteMongoshWithPort(ctx, podName, namespace, command, r.port)
	if err != nil {
		return fmt.Errorf("failed to add member: %w", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("rs.add failed: %s", result.Stderr)
	}

	return nil
}

// RemoveMember removes a member from the replica set
func (r *ReplicaSetManager) RemoveMember(ctx context.Context, podName, namespace, hostToRemove string) error {
	command := fmt.Sprintf("rs.remove(%s)", jsString(hostToRemove))
	result, err := r.executor.ExecuteMongoshWithPort(ctx, podName, namespace, command, r.port)
	if err != nil {
		return fmt.Errorf("failed to remove member: %w", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("rs.remove failed: %s", result.Stderr)
	}

	return nil
}

// Reconfigure updates the replica set configuration
func (r *ReplicaSetManager) Reconfigure(ctx context.Context, podName, namespace string, config ReplicaSetConfig, force bool) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	command := fmt.Sprintf("rs.reconfig(%s, {force: %t})", string(configJSON), force)
	result, err := r.executor.ExecuteMongoshWithPort(ctx, podName, namespace, command, r.port)
	if err != nil {
		return fmt.Errorf("failed to reconfigure replica set: %w", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("rs.reconfig failed: %s", result.Stderr)
	}

	return nil
}

// GetConfig returns the current replica set configuration
func (r *ReplicaSetManager) GetConfig(ctx context.Context, podName, namespace string) (*ReplicaSetConfig, error) {
	result, err := r.executor.ExecuteMongoshJSONWithPort(ctx, podName, namespace, "rs.conf()", r.port)
	if err != nil {
		return nil, fmt.Errorf("failed to get replica set config: %w", err)
	}

	if result.ExitCode != 0 {
		return nil, fmt.Errorf("rs.conf() failed: %s", result.Stderr)
	}

	var config ReplicaSetConfig
	if err := json.Unmarshal([]byte(result.Stdout), &config); err != nil {
		return nil, fmt.Errorf("failed to parse replica set config: %w", err)
	}

	return &config, nil
}

// BuildReplicaSetConfig builds a replica set configuration for initialization
func BuildReplicaSetConfig(rsName, baseName, serviceName, namespace string, members int, port int) ReplicaSetConfig {
	config := ReplicaSetConfig{
		ID:      rsName,
		Members: make([]ReplicaSetMember, members),
	}

	for i := 0; i < members; i++ {
		podName := fmt.Sprintf("%s-%d", baseName, i)
		host := GetPodFQDN(podName, serviceName, namespace, port)
		config.Members[i] = ReplicaSetMember{
			ID:   i,
			Host: host,
		}
	}

	return config
}

// BuildConfigServerReplicaSetConfig builds a config server replica set configuration
func BuildConfigServerReplicaSetConfig(rsName, baseName, serviceName, namespace string, members int, port int) ReplicaSetConfig {
	return BuildReplicaSetConfig(rsName, baseName, serviceName, namespace, members, port)
}

// BuildShardReplicaSetConfig builds a shard replica set configuration
func BuildShardReplicaSetConfig(shardName, baseName, serviceName, namespace string, members int, port int) ReplicaSetConfig {
	return BuildReplicaSetConfig(shardName, baseName, serviceName, namespace, members, port)
}
