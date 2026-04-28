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

// BootstrapAdminUser는 RS init 직후 primary에 익명 connect로 접속해
// 첫 admin user를 생성한다. 이 시점은 mongod이 --auth + --replSet으로 실행
// 중이지만 user가 0명이라 익명 접근이 허용되는 유일한 창. createUser 후엔
// 모든 connect가 SCRAM 인증 필요.
//
// 멱등성: user가 이미 있으면(`already exists`) skip. 익명 호출이 인증
// 요구를 받으면(`requires authentication` / `Authentication failed`)
// 이미 user가 존재한다는 강한 신호이므로 동일하게 nil 반환.
//
// 호출자는 firstHost를 RS의 임의 멤버 FQDN으로 전달. primary 추적은 내부
// 책임. roles는 admin DB의 root.
func BootstrapAdminUser(ctx context.Context, firstHost, username, password string) error {
	anonClient, err := NewClient(ctx, ConnectOpts{Hosts: []string{firstHost}, Direct: true})
	if err != nil {
		if isAuthRequiredErr(err) {
			return nil
		}
		return fmt.Errorf("anonymous connect: %w", err)
	}
	defer disconnectQuiet(anonClient)

	var status ReplicaSetStatus
	if err := anonClient.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&status); err != nil {
		if isAuthRequiredErr(err) {
			return nil
		}
		return fmt.Errorf("rs.status: %w", err)
	}

	var primaryHost string
	for _, m := range status.Members {
		if m.StateStr == "PRIMARY" && m.Health == 1 {
			primaryHost = m.Name
			break
		}
	}
	if primaryHost == "" {
		return fmt.Errorf("no primary member in rs status")
	}

	primaryClient, err := NewClient(ctx, ConnectOpts{Hosts: []string{primaryHost}, Direct: true})
	if err != nil {
		if isAuthRequiredErr(err) {
			return nil
		}
		return fmt.Errorf("primary connect: %w", err)
	}
	defer disconnectQuiet(primaryClient)

	var res bson.M
	err = primaryClient.Database("admin").RunCommand(ctx, bson.D{
		{Key: "createUser", Value: username},
		{Key: "pwd", Value: password},
		{Key: "roles", Value: bson.A{bson.M{"role": "root", "db": "admin"}}},
	}).Decode(&res)
	if err != nil {
		if isUserAlreadyExistsErr(err) {
			return nil
		}
		return fmt.Errorf("createUser: %w", err)
	}
	return nil
}

// MongoDB 표준 server error codes (참고: replicaset.go의 notYetInitializedCode와
// 같은 분류). string match는 mongo-driver 메시지 포맷이 minor release에서
// 바뀌면 silent break가 발생하므로 typed mongo.ServerError 우선으로 검사한다.
const (
	mongoErrUnauthorized         = 13
	mongoErrAuthenticationFailed = 18
	mongoErrDuplicateKey         = 11000
	mongoErrUserAlreadyExists    = 51003
)

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
	exists, err := a.UserExistsInContainer(ctx, podName, namespace, container, username, "admin", port)
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
		Username: "admin",
		Password: password,
		Database: "admin",
		Roles: []UserRole{
			{Role: "root", DB: "admin"},
		},
	}
}

// ClusterAdminUser returns a cluster admin user configuration
func ClusterAdminUser(username, password string) MongoUser {
	return MongoUser{
		Username: username,
		Password: password,
		Database: "admin",
		Roles: []UserRole{
			{Role: "clusterAdmin", DB: "admin"},
			{Role: "userAdminAnyDatabase", DB: "admin"},
			{Role: "readWriteAnyDatabase", DB: "admin"},
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
