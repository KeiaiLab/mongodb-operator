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

	"go.mongodb.org/mongo-driver/v2/bson"
)

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
