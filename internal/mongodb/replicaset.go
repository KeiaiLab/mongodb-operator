/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package mongodb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ConnectFactory는 podName/namespace 기반으로 mongo.Client를 만드는 함수다.
// 호출자(controller)가 cluster context(serviceName, port, 자격증명)를 closure로
// 캡슐화해 주입한다. 매니저는 host FQDN 빌드와 자격증명을 알 필요가 없다.
//
// direct=true는 replica set discovery를 비활성화하고 단일 호스트에 직접
// 붙는다. 용도: replSetInitiate처럼 replica set이 아직 형성되지 않은 단계.
type ConnectFactory func(ctx context.Context, podName, namespace string, direct bool) (*mongo.Client, error)

// ReplicaSetConfig represents a MongoDB replica set configuration
type ReplicaSetConfig struct {
	ID      string             `json:"_id" bson:"_id"`
	Members []ReplicaSetMember `json:"members" bson:"members"`
	Version int                `json:"version,omitempty" bson:"version,omitempty"`
}

// ReplicaSetMember represents a member in a replica set
type ReplicaSetMember struct {
	ID          int     `json:"_id" bson:"_id"`
	Host        string  `json:"host" bson:"host"`
	Priority    float64 `json:"priority,omitempty" bson:"priority,omitempty"`
	Votes       int     `json:"votes,omitempty" bson:"votes,omitempty"`
	ArbiterOnly bool    `json:"arbiterOnly,omitempty" bson:"arbiterOnly,omitempty"`
	Hidden      bool    `json:"hidden,omitempty" bson:"hidden,omitempty"`
}

// ReplicaSetStatus represents the status of a replica set
type ReplicaSetStatus struct {
	Set     string                   `json:"set" bson:"set"`
	MyState int                      `json:"myState" bson:"myState"`
	Members []ReplicaSetMemberStatus `json:"members" bson:"members"`
	OK      int                      `json:"ok" bson:"ok"`
}

// ReplicaSetMemberStatus represents the status of a replica set member
type ReplicaSetMemberStatus struct {
	ID       int    `json:"_id" bson:"_id"`
	Name     string `json:"name" bson:"name"`
	Health   int    `json:"health" bson:"health"`
	State    int    `json:"state" bson:"state"`
	StateStr string `json:"stateStr" bson:"stateStr"`
	Uptime   int64  `json:"uptime" bson:"uptime"`
	Self     bool   `json:"self,omitempty" bson:"self,omitempty"`
}

// ReplicaSetManager manages MongoDB replica set operations via mongo-go-driver.
type ReplicaSetManager struct {
	connect ConnectFactory
}

// NewReplicaSetManagerWithFactory는 driver client factory를 주입받아 매니저를 만든다.
// 권장 생성자.
func NewReplicaSetManagerWithFactory(f ConnectFactory) *ReplicaSetManager {
	return &ReplicaSetManager{connect: f}
}

// NewReplicaSetManager — Deprecated: Executor 기반 동작은 제거됐다.
// NewReplicaSetManagerWithFactory를 사용하라. 이 함수는 호출 시 항상 에러.
func NewReplicaSetManager() (*ReplicaSetManager, error) {
	return nil, errors.New("NewReplicaSetManager는 deprecated. NewReplicaSetManagerWithFactory(factory) 사용")
}

// NewReplicaSetManagerWithPort — Deprecated.
func NewReplicaSetManagerWithPort(port int) (*ReplicaSetManager, error) {
	return nil, errors.New("NewReplicaSetManagerWithPort는 deprecated. NewReplicaSetManagerWithFactory 사용")
}

// notYetInitializedCode는 mongod이 replica set으로 초기화되지 않았을 때
// replSetGetStatus에서 반환하는 server error code이다.

// helloNoConfigInfoSubstr는 mongod이 RS init 전(`--replSet`로 떠 있으나
// replSetInitiate 미수신) 상태에서 hello 응답에 포함시키는 info 문구다.
// mongo 4.x ~ 8.x 일관 — server 메시지 변경 시 회귀 가드 단위 테스트가 잡는다.
const (
	helloNoConfigInfoSubstr = "valid replica set config"

	rsStateStrPrimary = "PRIMARY"
)

// IsInitialized는 RS 멤버 등록 여부를 hello 명령으로 판정한다.
//
// 왜 hello인가 — replSetGetStatus는 인증 활성 mongod에서 user 0명일 때조차
// Unauthorized(13)/AuthenticationFailed(18)/NotYetInitialized(94) 중 어떤 것이든
// 반환할 수 있어 *서버 빌드/구성에 따라 분류 모호*. hello는 인증 비활성이고
// pre-init 상태와 RSGhost(config propagate 진행) 상태를 명확히 구분한다.
//
// 판정 규칙(mongo 4.x ~ 8.x 공통):
//   - hello.info == "Does not have a valid replica set config"  → not initialized
//   - hello.setName 존재                                          → initialized
//   - 그 외(RSGhost transient, error)                              → not initialized
//
// RSGhost 상태(setName 부재 + info 무관) 또한 *멤버 정의 미수신*이므로 init
// 미완료로 본다. 이는 controller에 "한 번 더 시도하라"는 안전한 시그널이며,
// `Initiate`가 idempotent이므로 false positive 비용이 없다.
//
// 본 구현은 이전(replSetGetStatus 기반 + Unauthorized→true 분류)이 fresh
// mongod의 pre-init unauthenticated 응답을 init-completed로 잘못 분류해
// `Status.ReplicaSetInitialized=true`를 영속화시킨 회귀(2026-04-29)를 봉쇄한다.
func (r *ReplicaSetManager) IsInitialized(ctx context.Context, podName, namespace string) (bool, error) {
	c, err := r.connect(ctx, podName, namespace, true)
	if err != nil {
		return false, fmt.Errorf("connect for IsInitialized: %w", err)
	}
	defer disconnectQuiet(c)

	var hello bson.M
	if err := c.Database(adminUserDB).RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return false, fmt.Errorf("hello: %w", err)
	}
	return classifyHelloForRSInit(hello), nil
}

// classifyHelloForRSInit는 hello 응답에서 RS init 완료 여부를 판정한다.
// 단위 테스트로 분기 표를 회귀 가드한다.
func classifyHelloForRSInit(hello bson.M) bool {
	if info, ok := hello["info"].(string); ok && strings.Contains(info, helloNoConfigInfoSubstr) {
		return false
	}
	_, hasSetName := hello["setName"]
	return hasSetName
}

// Initiate initializes a new replica set with the given config (idempotent).
func (r *ReplicaSetManager) Initiate(ctx context.Context, podName, namespace string, config ReplicaSetConfig) error {
	initialized, err := r.IsInitialized(ctx, podName, namespace)
	if err != nil {
		return fmt.Errorf("check init: %w", err)
	}
	if initialized {
		return nil
	}

	c, err := r.connect(ctx, podName, namespace, true)
	if err != nil {
		return fmt.Errorf("connect for Initiate: %w", err)
	}
	defer disconnectQuiet(c)

	var result bson.M
	err = c.Database(adminUserDB).RunCommand(ctx, bson.D{{Key: "replSetInitiate", Value: config}}).Decode(&result)
	if err != nil {
		return fmt.Errorf("replSetInitiate: %w", err)
	}
	return nil
}

// GetStatus returns the current replica set status.
func (r *ReplicaSetManager) GetStatus(ctx context.Context, podName, namespace string) (*ReplicaSetStatus, error) {
	c, err := r.connect(ctx, podName, namespace, true)
	if err != nil {
		return nil, fmt.Errorf("connect for GetStatus: %w", err)
	}
	defer disconnectQuiet(c)

	var status ReplicaSetStatus
	if err := c.Database(adminUserDB).RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&status); err != nil {
		return nil, fmt.Errorf("replSetGetStatus: %w", err)
	}
	return &status, nil
}

// GetPrimaryPod returns the pod name of the primary member.
// host 형식 "<pod>.<service>.<ns>.svc.cluster.local:<port>"의 첫 segment를 pod name으로 본다.
func (r *ReplicaSetManager) GetPrimaryPod(ctx context.Context, podName, namespace string) (string, error) {
	status, err := r.GetStatus(ctx, podName, namespace)
	if err != nil {
		return "", err
	}
	for _, m := range status.Members {
		if m.StateStr == rsStateStrPrimary {
			parts := strings.Split(m.Name, ".")
			if len(parts) > 0 {
				return parts[0], nil
			}
		}
	}
	return "", fmt.Errorf("no primary found")
}

// HasPrimary checks if the replica set has an elected primary.
func (r *ReplicaSetManager) HasPrimary(ctx context.Context, podName, namespace string) (bool, error) {
	status, err := r.GetStatus(ctx, podName, namespace)
	if err != nil {
		return false, err
	}
	for _, m := range status.Members {
		if m.StateStr == rsStateStrPrimary && m.Health == 1 {
			return true, nil
		}
	}
	return false, nil
}

// WaitForPrimary waits until a primary is elected (uses context for timeout).
// busy-loop 방지를 위해 2초 ticker로 폴링.
func (r *ReplicaSetManager) WaitForPrimary(ctx context.Context, podName, namespace string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			has, err := r.HasPrimary(ctx, podName, namespace)
			if err == nil && has {
				return nil
			}
		}
	}
}

// AddMember adds a new member to the replica set via replSetReconfig.
func (r *ReplicaSetManager) AddMember(ctx context.Context, podName, namespace, newHost string, arbiterOnly bool) error {
	cfg, err := r.GetConfig(ctx, podName, namespace)
	if err != nil {
		return fmt.Errorf("get config: %w", err)
	}
	maxID := -1
	for _, m := range cfg.Members {
		if m.Host == newHost {
			return nil // 이미 멤버 — idempotent
		}
		if m.ID > maxID {
			maxID = m.ID
		}
	}
	cfg.Members = append(cfg.Members, ReplicaSetMember{
		ID:          maxID + 1,
		Host:        newHost,
		ArbiterOnly: arbiterOnly,
	})
	cfg.Version++
	return r.Reconfigure(ctx, podName, namespace, *cfg, false)
}

// RemoveMember removes a member from the replica set via replSetReconfig.
func (r *ReplicaSetManager) RemoveMember(ctx context.Context, podName, namespace, hostToRemove string) error {
	cfg, err := r.GetConfig(ctx, podName, namespace)
	if err != nil {
		return fmt.Errorf("get config: %w", err)
	}
	filtered := cfg.Members[:0]
	removed := false
	for _, m := range cfg.Members {
		if m.Host == hostToRemove {
			removed = true
			continue
		}
		filtered = append(filtered, m)
	}
	if !removed {
		return nil // 이미 없음 — idempotent
	}
	cfg.Members = filtered
	cfg.Version++
	return r.Reconfigure(ctx, podName, namespace, *cfg, false)
}

// Reconfigure updates the replica set configuration.
func (r *ReplicaSetManager) Reconfigure(ctx context.Context, podName, namespace string, config ReplicaSetConfig, force bool) error {
	c, err := r.connect(ctx, podName, namespace, true)
	if err != nil {
		return fmt.Errorf("connect for Reconfigure: %w", err)
	}
	defer disconnectQuiet(c)

	var result bson.M
	err = c.Database(adminUserDB).RunCommand(ctx, bson.D{
		{Key: "replSetReconfig", Value: config},
		{Key: "force", Value: force},
	}).Decode(&result)
	if err != nil {
		return fmt.Errorf("replSetReconfig: %w", err)
	}
	return nil
}

// GetConfig returns the current replica set configuration.
func (r *ReplicaSetManager) GetConfig(ctx context.Context, podName, namespace string) (*ReplicaSetConfig, error) {
	c, err := r.connect(ctx, podName, namespace, true)
	if err != nil {
		return nil, fmt.Errorf("connect for GetConfig: %w", err)
	}
	defer disconnectQuiet(c)

	type confResult struct {
		Config ReplicaSetConfig `bson:"config"`
		OK     float64          `bson:"ok"`
	}
	var res confResult
	if err := c.Database(adminUserDB).RunCommand(ctx, bson.D{{Key: "replSetGetConfig", Value: 1}}).Decode(&res); err != nil {
		return nil, fmt.Errorf("replSetGetConfig: %w", err)
	}
	return &res.Config, nil
}

// disconnectQuiet은 client.Disconnect의 에러를 무시한다. defer 안에서 사용.
// 진행 중인 작업의 결과 에러를 덮어쓰지 않기 위함.
func disconnectQuiet(c *mongo.Client) {
	_ = c.Disconnect(context.Background())
}

// BuildReplicaSetConfig builds a replica set configuration for initialization.
func BuildReplicaSetConfig(rsName, baseName, serviceName, namespace string, members int, port int) ReplicaSetConfig {
	config := ReplicaSetConfig{
		ID:      rsName,
		Members: make([]ReplicaSetMember, members),
	}
	for i := 0; i < members; i++ {
		podName := fmt.Sprintf("%s-%d", baseName, i)
		host := GetPodFQDN(podName, serviceName, namespace, port)
		config.Members[i] = ReplicaSetMember{ID: i, Host: host}
	}
	return config
}

// BuildConfigServerReplicaSetConfig builds a config server replica set configuration.
func BuildConfigServerReplicaSetConfig(rsName, baseName, serviceName, namespace string, members int, port int) ReplicaSetConfig {
	return BuildReplicaSetConfig(rsName, baseName, serviceName, namespace, members, port)
}

// BuildShardReplicaSetConfig builds a shard replica set configuration.
func BuildShardReplicaSetConfig(shardName, baseName, serviceName, namespace string, members int, port int) ReplicaSetConfig {
	return BuildReplicaSetConfig(shardName, baseName, serviceName, namespace, members, port)
}

// NewPodConnectFactory는 controller가 흔히 만드는 ConnectFactory를 한 줄로 만든다.
// serviceName/port/자격증명을 closure에 캡슐화해 podName, namespace만 받는 형태로 노출한다.
//
// 이 패턴은 *headless service + StatefulSet* (RS / cfg / shard) 전용이다 — pod의
// 안정 hostname `<pod>.<svc>.<ns>...`을 DNS로 해석한다.
func NewPodConnectFactory(serviceName string, port int, username, password, authDB string) ConnectFactory {
	return func(ctx context.Context, podName, namespace string, direct bool) (*mongo.Client, error) {
		host := GetPodFQDN(podName, serviceName, namespace, port)
		return NewClient(ctx, ConnectOpts{
			Hosts:    []string{host},
			Username: username,
			Password: password,
			AuthDB:   authDB,
			Direct:   direct,
		})
	}
}

// NewServiceConnectFactory는 ClusterIP/headless Service DNS로 connect하는
// ConnectFactory다. mongos(Deployment + ClusterIP)처럼 안정된 pod hostname이
// 없는 컴포넌트에 사용한다.
//
// host 형식: `<serviceName>.<namespace>.svc.cluster.local:<port>`. podName/namespace
// 인자는 무시한다(ConnectFactory 시그니처 호환을 위해 받는다).
func NewServiceConnectFactory(serviceName, namespace string, port int, username, password, authDB string) ConnectFactory {
	host := fmt.Sprintf("%s.%s.svc.cluster.local:%d", serviceName, namespace, port)
	return func(ctx context.Context, _, _ string, direct bool) (*mongo.Client, error) {
		return NewClient(ctx, ConnectOpts{
			Hosts:    []string{host},
			Username: username,
			Password: password,
			AuthDB:   authDB,
			Direct:   direct,
		})
	}
}
