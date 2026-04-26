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

// UserRole represents a MongoDB role assignment
type UserRole struct {
	Role string `json:"role"`
	DB   string `json:"db"`
}

// MongoUser represents a MongoDB user
type MongoUser struct {
	Username string     `json:"user"`
	Password string     `json:"pwd"`
	Database string     `json:"db"` // Database where user is defined
	Roles    []UserRole `json:"roles"`
}

// AuthManager manages MongoDB authentication
type AuthManager struct {
	executor *Executor
}

// NewAuthManager creates a new auth manager
func NewAuthManager() (*AuthManager, error) {
	exec, err := NewExecutor()
	if err != nil {
		return nil, err
	}
	return &AuthManager{executor: exec}, nil
}

// NewAuthManagerWithExecutor creates a new auth manager with provided executor
func NewAuthManagerWithExecutor(exec *Executor) *AuthManager {
	return &AuthManager{executor: exec}
}

// CreateAdminUser creates the initial admin user using localhost exception
// This must be run when no users exist (localhost exception allows first user creation)
func (a *AuthManager) CreateAdminUser(ctx context.Context, podName, namespace, username, password string) error {
	return a.CreateAdminUserInContainer(ctx, podName, namespace, "mongodb", username, password, 27017)
}

// CreateAdminUserInContainer creates the initial admin user in a specified container
func (a *AuthManager) CreateAdminUserInContainer(ctx context.Context, podName, namespace, container, username, password string, port int) error {
	roles := []UserRole{
		{Role: "root", DB: "admin"},
	}

	rolesJSON, err := json.Marshal(roles)
	if err != nil {
		return fmt.Errorf("failed to marshal roles: %w", err)
	}

	// Use localhost exception for first user creation.
	// 사용자 제어 값(username/password)은 jsString으로 JS-safe literal로 인코딩한다.
	command := fmt.Sprintf(`
		db.getSiblingDB("admin").createUser({
			user: %s,
			pwd: %s,
			roles: %s
		})
	`, jsString(username), jsString(password), string(rolesJSON))

	result, err := a.executor.ExecuteMongoshInContainer(ctx, podName, namespace, container, command, port)
	if err != nil {
		return fmt.Errorf("failed to create admin user: %w", err)
	}

	// Check if user already exists
	if strings.Contains(result.Stderr, "already exists") {
		return nil // User already exists, not an error
	}

	if result.ExitCode != 0 && !strings.Contains(result.Stdout, "ok") {
		return fmt.Errorf("createUser failed: stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	}

	return nil
}

// CreateUser creates a new MongoDB user (requires authentication)
func (a *AuthManager) CreateUser(ctx context.Context, podName, namespace, adminUser, adminPassword string, user MongoUser) error {
	rolesJSON, err := json.Marshal(user.Roles)
	if err != nil {
		return fmt.Errorf("failed to marshal roles: %w", err)
	}

	command := fmt.Sprintf(`
		db.getSiblingDB(%s).createUser({
			user: %s,
			pwd: %s,
			roles: %s
		})
	`, jsString(user.Database), jsString(user.Username), jsString(user.Password), string(rolesJSON))

	result, err := a.executor.ExecuteMongoshWithAuth(ctx, podName, namespace, adminUser, adminPassword, "admin", command)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	// Check if user already exists
	if strings.Contains(result.Stderr, "already exists") {
		return nil
	}

	if result.ExitCode != 0 && !strings.Contains(result.Stdout, "ok") {
		return fmt.Errorf("createUser failed: stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	}

	return nil
}

// UserExists checks if a user exists
func (a *AuthManager) UserExists(ctx context.Context, podName, namespace, username, database string) (bool, error) {
	return a.UserExistsInContainer(ctx, podName, namespace, "mongodb", username, database, 27017)
}

// UserExistsInContainer checks if a user exists in a specified container
func (a *AuthManager) UserExistsInContainer(ctx context.Context, podName, namespace, container, username, database string, port int) (bool, error) {
	command := fmt.Sprintf(`
		const user = db.getSiblingDB(%s).getUser(%s);
		user !== null
	`, jsString(database), jsString(username))

	result, err := a.executor.ExecuteMongoshInContainer(ctx, podName, namespace, container, command, port)
	if err != nil {
		return false, fmt.Errorf("failed to check user: %w", err)
	}

	return strings.TrimSpace(result.Stdout) == "true", nil
}

// UserExistsWithAuth checks if a user exists (with authentication)
func (a *AuthManager) UserExistsWithAuth(ctx context.Context, podName, namespace, adminUser, adminPassword, username, database string) (bool, error) {
	command := fmt.Sprintf(`
		const user = db.getSiblingDB(%s).getUser(%s);
		user !== null
	`, jsString(database), jsString(username))

	result, err := a.executor.ExecuteMongoshWithAuth(ctx, podName, namespace, adminUser, adminPassword, "admin", command)
	if err != nil {
		return false, fmt.Errorf("failed to check user: %w", err)
	}

	return strings.TrimSpace(result.Stdout) == "true", nil
}

// UpdatePassword updates a user's password
func (a *AuthManager) UpdatePassword(ctx context.Context, podName, namespace, adminUser, adminPassword, targetUser, targetDB, newPassword string) error {
	command := fmt.Sprintf(`
		db.getSiblingDB(%s).changeUserPassword(%s, %s)
	`, jsString(targetDB), jsString(targetUser), jsString(newPassword))

	result, err := a.executor.ExecuteMongoshWithAuth(ctx, podName, namespace, adminUser, adminPassword, "admin", command)
	if err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("changeUserPassword failed: %s", result.Stderr)
	}

	return nil
}

// GrantRoles grants additional roles to a user
func (a *AuthManager) GrantRoles(ctx context.Context, podName, namespace, adminUser, adminPassword, targetUser, targetDB string, roles []UserRole) error {
	rolesJSON, err := json.Marshal(roles)
	if err != nil {
		return fmt.Errorf("failed to marshal roles: %w", err)
	}

	command := fmt.Sprintf(`
		db.getSiblingDB(%s).grantRolesToUser(%s, %s)
	`, jsString(targetDB), jsString(targetUser), string(rolesJSON))

	result, err := a.executor.ExecuteMongoshWithAuth(ctx, podName, namespace, adminUser, adminPassword, "admin", command)
	if err != nil {
		return fmt.Errorf("failed to grant roles: %w", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("grantRolesToUser failed: %s", result.Stderr)
	}

	return nil
}

// RevokeRoles revokes roles from a user
func (a *AuthManager) RevokeRoles(ctx context.Context, podName, namespace, adminUser, adminPassword, targetUser, targetDB string, roles []UserRole) error {
	rolesJSON, err := json.Marshal(roles)
	if err != nil {
		return fmt.Errorf("failed to marshal roles: %w", err)
	}

	command := fmt.Sprintf(`
		db.getSiblingDB(%s).revokeRolesFromUser(%s, %s)
	`, jsString(targetDB), jsString(targetUser), string(rolesJSON))

	result, err := a.executor.ExecuteMongoshWithAuth(ctx, podName, namespace, adminUser, adminPassword, "admin", command)
	if err != nil {
		return fmt.Errorf("failed to revoke roles: %w", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("revokeRolesFromUser failed: %s", result.Stderr)
	}

	return nil
}

// DropUser removes a user
func (a *AuthManager) DropUser(ctx context.Context, podName, namespace, adminUser, adminPassword, targetUser, targetDB string) error {
	command := fmt.Sprintf(`
		db.getSiblingDB(%s).dropUser(%s)
	`, jsString(targetDB), jsString(targetUser))

	result, err := a.executor.ExecuteMongoshWithAuth(ctx, podName, namespace, adminUser, adminPassword, "admin", command)
	if err != nil {
		return fmt.Errorf("failed to drop user: %w", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("dropUser failed: %s", result.Stderr)
	}

	return nil
}

// Authenticate tests authentication with given credentials
func (a *AuthManager) Authenticate(ctx context.Context, podName, namespace, username, password, authDB string) error {
	command := "db.adminCommand('ping')"
	result, err := a.executor.ExecuteMongoshWithAuth(ctx, podName, namespace, username, password, authDB, command)
	if err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	if result.ExitCode != 0 {
		return fmt.Errorf("authentication failed: %s", result.Stderr)
	}

	return nil
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
