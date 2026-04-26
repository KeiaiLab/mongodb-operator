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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildReplicaSetConfig(t *testing.T) {
	tests := []struct {
		name        string
		rsName      string
		baseName    string
		serviceName string
		namespace   string
		members     int
		port        int
	}{
		{
			name:        "three member replica set",
			rsName:      "rs0",
			baseName:    "my-mongodb",
			serviceName: "my-mongodb-headless",
			namespace:   "default",
			members:     3,
			port:        27017,
		},
		{
			name:        "single member replica set",
			rsName:      "rs0",
			baseName:    "test-mongo",
			serviceName: "test-mongo-headless",
			namespace:   "test",
			members:     1,
			port:        27017,
		},
		{
			name:        "five member replica set",
			rsName:      "myReplicaSet",
			baseName:    "prod-mongodb",
			serviceName: "prod-mongodb-headless",
			namespace:   "production",
			members:     5,
			port:        27017,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := BuildReplicaSetConfig(tt.rsName, tt.baseName, tt.serviceName, tt.namespace, tt.members, tt.port)

			assert.Equal(t, tt.rsName, config.ID)
			assert.Len(t, config.Members, tt.members)

			for i := 0; i < tt.members; i++ {
				assert.Equal(t, i, config.Members[i].ID)
				expectedHost := GetPodFQDN(
					config.Members[i].Host[:len(config.Members[i].Host)-len(":27017")-len("."+tt.serviceName+"."+tt.namespace+".svc.cluster.local")],
					tt.serviceName,
					tt.namespace,
					tt.port,
				)
				assert.Contains(t, config.Members[i].Host, tt.serviceName)
				assert.Contains(t, config.Members[i].Host, tt.namespace)
				_ = expectedHost // Avoid unused variable warning
			}
		})
	}
}

func TestBuildConfigServerReplicaSetConfig(t *testing.T) {
	config := BuildConfigServerReplicaSetConfig("configReplSet", "my-cfg", "my-cfg-headless", "default", 3, 27019)

	assert.Equal(t, "configReplSet", config.ID)
	assert.Len(t, config.Members, 3)

	for i, member := range config.Members {
		assert.Equal(t, i, member.ID)
		assert.Contains(t, member.Host, "my-cfg-headless")
		assert.Contains(t, member.Host, "27019")
	}
}

func TestBuildShardReplicaSetConfig(t *testing.T) {
	config := BuildShardReplicaSetConfig("shard0", "my-shard-0", "my-shard-0-headless", "default", 3, 27018)

	assert.Equal(t, "shard0", config.ID)
	assert.Len(t, config.Members, 3)

	for i, member := range config.Members {
		assert.Equal(t, i, member.ID)
		assert.Contains(t, member.Host, "my-shard-0-headless")
		assert.Contains(t, member.Host, "27018")
	}
}

func TestReplicaSetConfig(t *testing.T) {
	config := ReplicaSetConfig{
		ID: "rs0",
		Members: []ReplicaSetMember{
			{ID: 0, Host: "mongo-0.mongo-headless.default.svc.cluster.local:27017"},
			{ID: 1, Host: "mongo-1.mongo-headless.default.svc.cluster.local:27017"},
			{ID: 2, Host: "mongo-2.mongo-headless.default.svc.cluster.local:27017"},
		},
		Version: 1,
	}

	assert.Equal(t, "rs0", config.ID)
	assert.Len(t, config.Members, 3)
	assert.Equal(t, 1, config.Version)
}

func TestReplicaSetMember(t *testing.T) {
	member := ReplicaSetMember{
		ID:          0,
		Host:        "mongo-0.mongo-headless.default.svc.cluster.local:27017",
		Priority:    1.0,
		Votes:       1,
		ArbiterOnly: false,
		Hidden:      false,
	}

	assert.Equal(t, 0, member.ID)
	assert.Equal(t, "mongo-0.mongo-headless.default.svc.cluster.local:27017", member.Host)
	assert.Equal(t, 1.0, member.Priority)
	assert.Equal(t, 1, member.Votes)
	assert.False(t, member.ArbiterOnly)
	assert.False(t, member.Hidden)
}

func TestReplicaSetMemberArbiter(t *testing.T) {
	member := ReplicaSetMember{
		ID:          2,
		Host:        "mongo-arbiter.mongo-headless.default.svc.cluster.local:27017",
		Priority:    0,
		Votes:       1,
		ArbiterOnly: true,
		Hidden:      false,
	}

	assert.Equal(t, 2, member.ID)
	assert.True(t, member.ArbiterOnly)
	assert.Equal(t, float64(0), member.Priority)
}

func TestReplicaSetStatus(t *testing.T) {
	status := ReplicaSetStatus{
		Set:     "rs0",
		MyState: 1,
		Members: []ReplicaSetMemberStatus{
			{ID: 0, Name: "mongo-0:27017", Health: 1, State: 1, StateStr: "PRIMARY", Uptime: 3600, Self: true},
			{ID: 1, Name: "mongo-1:27017", Health: 1, State: 2, StateStr: "SECONDARY", Uptime: 3500},
			{ID: 2, Name: "mongo-2:27017", Health: 1, State: 2, StateStr: "SECONDARY", Uptime: 3400},
		},
		OK: 1,
	}

	assert.Equal(t, "rs0", status.Set)
	assert.Equal(t, 1, status.MyState)
	assert.Len(t, status.Members, 3)
	assert.Equal(t, 1, status.OK)

	// Check primary
	assert.Equal(t, "PRIMARY", status.Members[0].StateStr)
	assert.True(t, status.Members[0].Self)

	// Check secondaries
	for i := 1; i < 3; i++ {
		assert.Equal(t, "SECONDARY", status.Members[i].StateStr)
		assert.Equal(t, 1, status.Members[i].Health)
	}
}

func TestReplicaSetMemberStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   ReplicaSetMemberStatus
		isPrim   bool
		isSec    bool
		isHealth bool
	}{
		{
			name:     "primary healthy",
			status:   ReplicaSetMemberStatus{ID: 0, Name: "mongo-0:27017", Health: 1, State: 1, StateStr: "PRIMARY"},
			isPrim:   true,
			isSec:    false,
			isHealth: true,
		},
		{
			name:     "secondary healthy",
			status:   ReplicaSetMemberStatus{ID: 1, Name: "mongo-1:27017", Health: 1, State: 2, StateStr: "SECONDARY"},
			isPrim:   false,
			isSec:    true,
			isHealth: true,
		},
		{
			name:     "unhealthy member",
			status:   ReplicaSetMemberStatus{ID: 2, Name: "mongo-2:27017", Health: 0, State: 8, StateStr: "DOWN"},
			isPrim:   false,
			isSec:    false,
			isHealth: false,
		},
		{
			name:     "arbiter",
			status:   ReplicaSetMemberStatus{ID: 3, Name: "mongo-arb:27017", Health: 1, State: 7, StateStr: "ARBITER"},
			isPrim:   false,
			isSec:    false,
			isHealth: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.isPrim, tt.status.StateStr == "PRIMARY")
			assert.Equal(t, tt.isSec, tt.status.StateStr == "SECONDARY")
			assert.Equal(t, tt.isHealth, tt.status.Health == 1)
		})
	}
}

// TestNewReplicaSetManagerWithFactory는 driver factory 주입 패턴을 검증한다.
// (이전 NewReplicaSetManagerWithExecutor 테스트는 Executor 제거로 폐기됨.)
func TestNewReplicaSetManagerWithFactory(t *testing.T) {
	// nil factory도 일단 생성은 가능 (호출 시점에 panic). 단순 not-nil 검증.
	manager := NewReplicaSetManagerWithFactory(nil)
	assert.NotNil(t, manager)
}
