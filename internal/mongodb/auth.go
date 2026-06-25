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

// BootstrapAdminUser는 RS init 직후 첫 admin user를 생성한다. mongod이
// --auth + --replSet으로 실행 중이지만 user가 0명이라 localhost-exception이
// 살아있는 좁은 창에서만 성공. createUser 후엔 모든 connect가 SCRAM 인증 필요.
//
// 라우팅: Direct=false로 anon connect — driver가 firstHost를 seed로 RS
// topology를 discovery한 뒤 createUser write를 자동 primary 라우팅한다.
// 이는 두 가지 이유로 직접 primary 추적 방식보다 우월하다.
//
//  1. 0-user 상태에서 익명 replSetGetStatus는 mongod 빌드/구성에 따라
//     Unauthorized로 거부될 수 있다. preflight를 거치지 않고 곧장 createUser를
//     던지면 localhost-exception 창을 정확히 활용한다.
//  2. primary가 first pod이 아닐 때 직접 primary 추적은 1회 실패 위험을
//     도입한다. driver의 server selection은 정상 RS topology에서 ms 단위로
//     primary를 찾는다.
//
// 멱등성:
//   - user가 이미 존재 → idempotent skip (UserAlreadyExists / DuplicateKey).
//   - 익명 connect/discovery가 인증 요구로 거부 → user가 이미 있고
//     localhost-exception이 닫힌 상태로 추정, idempotent skip.
//   - createUser 자체가 인증 요구로 거부 → 동일하게 idempotent skip.
//
// 호출자는 firstHost를 RS의 임의 멤버 FQDN(host:port)으로 전달. roles는
// admin DB의 root.
func BootstrapAdminUser(ctx context.Context, firstHost, username, password string) error {
	// Timeout=25s — fresh RS의 primary 선출(보통 5–10s)을 기다리며 driver의
	// server selection이 PRIMARY를 찾을 수 있도록 default(10s)보다 충분히 길게.
	anonClient, err := NewClient(ctx, ConnectOpts{
		Hosts:   []string{firstHost},
		Direct:  false,
		Timeout: 25 * time.Second,
	})
	if err != nil {
		if isAuthRequiredErr(err) {
			return nil
		}
		return fmt.Errorf("anonymous connect: %w", err)
	}
	defer disconnectQuiet(anonClient)

	var res bson.M
	err = anonClient.Database(adminUserDB).RunCommand(ctx, bson.D{
		{Key: "createUser", Value: username},
		{Key: "pwd", Value: password},
		{Key: "roles", Value: bson.A{bson.M{"role": "root", "db": adminUserDB}}},
	}).Decode(&res)
	if err != nil {
		if isUserAlreadyExistsErr(err) || isAuthRequiredErr(err) {
			return nil
		}
		return fmt.Errorf("createUser: %w", err)
	}
	return nil
}

// MongoDB 표준 server error codes (참고: replicaset.go의 notYetInitializedCode와
// 같은 분류). string match는 mongo-driver 메시지 포맷이 minor release에서
// 바뀌면 silent break가 발생하므로 typed mongo.ServerError 우선으로 검사한다.
// SCRAM mechanism 이름. mongot 은 SCRAM-SHA-1 을 쓰므로 searchCoordinator user 는 둘 다 보유해야 함.
const (
	scramSHA1   = "SCRAM-SHA-1"
	scramSHA256 = "SCRAM-SHA-256"
)

const (
	mongoErrUnauthorized         = 13
	mongoErrAuthenticationFailed = 18
	mongoErrUserNotFound         = 11
	mongoErrDuplicateKey         = 11000
	mongoErrUserAlreadyExists    = 51003
)

// IsAuthRequiredErr는 isAuthRequiredErr의 export wrapper. controller 패키지에서
// pre-bootstrap primary 체크 분기 결정에 사용 (ADR-0005 보강).
func IsAuthRequiredErr(err error) bool {
	return isAuthRequiredErr(err)
}

// isAuthRequiredErr는 connect/command가 인증 요구·실패로 거부됐는지 검사한다.
// typed mongo.ServerError로 wrapping된 경우 code(13/18)를 우선 보고, TLS
// handshake처럼 typed wrapping이 없는 경로에서는 server response message를
// fallback으로 본다.
func isAuthRequiredErr(err error) bool {
	if err == nil {
		return false
	}
	var srvErr mongo.ServerError
	if errors.As(err, &srvErr) {
		if srvErr.HasErrorCode(mongoErrUnauthorized) || srvErr.HasErrorCode(mongoErrAuthenticationFailed) {
			return true
		}
	}
	s := err.Error()
	return strings.Contains(s, "requires authentication") ||
		strings.Contains(s, "Authentication failed") ||
		strings.Contains(s, "AuthenticationFailed")
}

// isUserAlreadyExistsErr는 createUser가 중복 user로 거부됐는지 검사한다.
// MongoDB는 server version에 따라 11000(DuplicateKey) 또는 51003
// (UserAlreadyExists) 중 하나를 반환한다. typed 우선, message fallback.
func isUserAlreadyExistsErr(err error) bool {
	if err == nil {
		return false
	}
	var srvErr mongo.ServerError
	if errors.As(err, &srvErr) {
		if srvErr.HasErrorCode(mongoErrDuplicateKey) || srvErr.HasErrorCode(mongoErrUserAlreadyExists) {
			return true
		}
	}
	s := err.Error()
	return strings.Contains(s, "already exists")
}

// EnsureSearchCoordinatorUser — mongot sync 용 searchCoordinator user 를 admin 인증된 client 로
// 생성/보정한다. mongot 은 SCRAM-SHA-1 을 쓰므로 SCRAM-SHA-1 + SCRAM-SHA-256 두 mechanism 모두로
// 만들어야 한다(SHA-256 단독이면 mongot 인증 실패). 멱등: 이미 존재하면 updateUser 로 pwd +
// mechanisms 를 보정한다(self-heal — 기존이 SHA-256 단독이어도 SHA-1 추가). mechanisms *확장* 은
// server 가 credential 을 재계산하므로 pwd 동반 필수. client 는 호출자가 disconnect 한다.
func EnsureSearchCoordinatorUser(ctx context.Context, c *mongo.Client, username, password string) error {
	if password == "" {
		return fmt.Errorf("searchCoordinator user %q: 빈 password — passwordless user 생성 거부", username)
	}
	// 비번 동기화 보장: usersInfo(mechanisms+role)만 검사하던 precheck 를 제거했다 — 그 precheck 는
	// *비번 drift 를 감지 못해* config server 의 search-sync 비번이 secret 과 어긋나도(이미 dual SCRAM +
	// role 보유 시) skip → mongot 의 router(config server) 인증 실패로 index catalog 조회 불가
	// (prod sharded search 2026-06-24 RCA — shard-local 비번은 맞아 replicaSet sync 는 정상이나
	// config server 비번만 drift). 매 reconcile createUser→(존재 시)updateUser 로 pwd+mechanisms+roles
	// 를 secret 기준 보정한다(멱등 — 동일 pwd 면 mongot 재인증 무영향, updateUser oplog write 는
	// search reconcile 30s 빈도상 미미).
	mechanisms := bson.A{scramSHA1, scramSHA256}
	roles := bson.A{bson.M{"role": "searchCoordinator", "db": adminUserDB}}
	var res bson.M
	err := c.Database(adminUserDB).RunCommand(ctx, bson.D{
		{Key: "createUser", Value: username},
		{Key: "pwd", Value: password},
		{Key: "mechanisms", Value: mechanisms},
		{Key: "roles", Value: roles},
	}).Decode(&res)
	if err == nil {
		return nil
	}
	if !isUserAlreadyExistsErr(err) {
		return fmt.Errorf("createUser %q (searchCoordinator): %w", username, err)
	}
	// 이미 존재하나 precheck 불충족(SHA-1 누락 / role 누락 등) → pwd+mechanisms+roles 보정.
	// createUser 는 mechanisms/roles 를 갱신 못하므로 updateUser 로 self-heal. mechanisms 확장은
	// server 가 credential 을 재계산하므로 pwd 동반 필수. roles 는 searchCoordinator 단일로 정규화.
	if err := c.Database(adminUserDB).RunCommand(ctx, bson.D{
		{Key: "updateUser", Value: username},
		{Key: "pwd", Value: password},
		{Key: "mechanisms", Value: mechanisms},
		{Key: "roles", Value: roles},
	}).Decode(&res); err != nil {
		return fmt.Errorf("updateUser %q (searchCoordinator 보정): %w", username, err)
	}
	return nil
}

// DropSearchCoordinatorUser — searchCoordinator(search-sync) user 를 admin 인증된 client 로 삭제한다
// (MongoDBSearch CR 삭제 finalizer cleanup, EnsureSearchCoordinatorUser sister). 멱등: user 부재
// (UserNotFound)면 성공 처리. source 가 sharded 면 호출자가 mongos(config server)+각 shard 에 대칭
// 호출(생성 경로 ①+② 거울) — 미삭제 시 searchCoordinator 특권 user 가 source 에 영구 orphan.
func DropSearchCoordinatorUser(ctx context.Context, c *mongo.Client, username string) error {
	var res bson.M
	if err := c.Database(adminUserDB).RunCommand(ctx, bson.D{{Key: "dropUser", Value: username}}).Decode(&res); err != nil {
		if isUserNotFoundErr(err) {
			return nil // 멱등 — 이미 없음
		}
		return fmt.Errorf("dropUser %q (searchCoordinator): %w", username, err)
	}
	return nil
}

// isUserNotFoundErr는 dropUser 가 미존재 user 로 거부됐는지 검사한다(UserNotFound=11). typed code 만
// 신뢰한다 — message substring fallback("not found")은 'database not found'/DNS 'host not found' 등
// dropUser 무관 실패까지 멱등 성공으로 오인해 *실제 drop 실패를 삼키고 finalizer 를 해제 → user leak*
// 위험이 있어 제거(adversarial review). 현대 MongoDB dropUser 는 미존재 user 에 안정적으로 code 11 반환.
func isUserNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	var srvErr mongo.ServerError
	if errors.As(err, &srvErr) {
		return srvErr.HasErrorCode(mongoErrUserNotFound)
	}
	return false
}

// UserRole represents a MongoDB role assignment
type UserRole struct {
	Role string `json:"role" bson:"role"`
	DB   string `json:"db" bson:"db"`
}

// MongoUser represents a MongoDB user
type MongoUser struct {
	Username string     `json:"user" bson:"user"`
	Password string     `json:"pwd" bson:"pwd"`
	Database string     `json:"db" bson:"db"`
	Roles    []UserRole `json:"roles" bson:"roles"`
}

// AuthManager는 mongo-go-driver를 사용해 MongoDB 인증/사용자 관리를 수행한다.
//
// 본 매니저의 핵심 의도 변화: 첫 admin user는 mongodb pod의 lifecycle.postStart
// hook이 만든다(Commit 2). 따라서 매니저의 CreateAdminUser는 더 이상 user를
// "생성"하지 않고 "이미 존재하는 admin user에 인증할 수 있는지 검증"한다.
//
// 시그니처 호환을 유지하기 위해 기존 메서드 이름과 인자는 그대로 두지만
// 내부 구현은 driver 기반이며, adminUser/adminPassword 인자는 무시된다
// (factory가 자격증명을 캡슐화).
type AuthManager struct {
	connect ConnectFactory
}

// NewAuthManagerWithFactory는 driver client factory를 주입받아 매니저를 만든다.
// 권장 생성자.
func NewAuthManagerWithFactory(f ConnectFactory) *AuthManager {
	return &AuthManager{connect: f}
}

// NewAuthManager — Deprecated. Executor 기반 동작은 제거됐다.
func NewAuthManager() (*AuthManager, error) {
	return nil, errors.New("NewAuthManager는 deprecated. NewAuthManagerWithFactory(factory) 사용")
}

// CreateAdminUser는 admin user에 인증할 수 있는지 검증한다.
//
// 이전 동작: localhost exception으로 mongosh exec → createUser.
// 새 동작: pod의 postStart bootstrap이 이미 user를 만들었다고 가정. driver로
// 인증 시도해 user 존재와 password 정확성을 동시 검증.
//
// adminUser/password 인자는 factory가 캡슐화한 자격증명과 동일해야 의미 있다.
// 호환성을 위해 시그니처 유지.
func (a *AuthManager) CreateAdminUser(ctx context.Context, podName, namespace, username, password string) error {
	return a.CreateAdminUserInContainer(ctx, podName, namespace, "mongodb", username, password, 27017)
}

// CreateAdminUserInContainer는 CreateAdminUser와 같지만 container 인자는 driver
// 모델에서 의미가 없으므로 무시된다. (mongosh exec 시대의 잔재)
func (a *AuthManager) CreateAdminUserInContainer(ctx context.Context, podName, namespace, container, username, password string, port int) error {
	exists, err := a.UserExistsInContainer(ctx, podName, namespace, container, username, adminUserDB, port)
	if err != nil {
		return fmt.Errorf("verify admin user: %w", err)
	}
	if !exists {
		return fmt.Errorf("admin user %q not found — pod의 lifecycle.postStart bootstrap이 실행되지 않았거나 실패함", username)
	}
	return nil
}

// UserExists checks if a user exists in the specified database.
func (a *AuthManager) UserExists(ctx context.Context, podName, namespace, username, database string) (bool, error) {
	return a.UserExistsInContainer(ctx, podName, namespace, "mongodb", username, database, 27017)
}

// UserExistsInContainer is identical to UserExists; container/port 인자는 무시된다.
func (a *AuthManager) UserExistsInContainer(ctx context.Context, podName, namespace, container, username, database string, port int) (bool, error) {
	c, err := a.connect(ctx, podName, namespace, true)
	if err != nil {
		return false, fmt.Errorf("connect for UserExists: %w", err)
	}
	defer disconnectQuiet(c)

	var result struct {
		Users []bson.M `bson:"users"`
		OK    float64  `bson:"ok"`
	}
	cmd := bson.D{
		{Key: "usersInfo", Value: bson.D{{Key: "user", Value: username}, {Key: "db", Value: database}}},
	}
	if err := c.Database(database).RunCommand(ctx, cmd).Decode(&result); err != nil {
		return false, fmt.Errorf("usersInfo: %w", err)
	}
	return len(result.Users) > 0, nil
}

// DefaultAdminUser returns the default admin user configuration
func DefaultAdminUser(password string) MongoUser {
	return MongoUser{
		Username: adminUserDB,
		Password: password,
		Database: adminUserDB,
		Roles: []UserRole{
			{Role: "root", DB: adminUserDB},
		},
	}
}

// ClusterAdminUser returns a cluster admin user configuration
func ClusterAdminUser(username, password string) MongoUser {
	return MongoUser{
		Username: username,
		Password: password,
		Database: adminUserDB,
		Roles: []UserRole{
			{Role: "clusterAdmin", DB: adminUserDB},
			{Role: "userAdminAnyDatabase", DB: adminUserDB},
			{Role: "readWriteAnyDatabase", DB: adminUserDB},
		},
	}
}

// ReadWriteUser returns a read-write user configuration for a specific database
func ReadWriteUser(username, password, database string) MongoUser {
	return MongoUser{
		Username: username,
		Password: password,
		Database: database,
		Roles: []UserRole{
			{Role: "readWrite", DB: database},
		},
	}
}
