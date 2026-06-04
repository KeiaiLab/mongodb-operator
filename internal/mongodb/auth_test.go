/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package mongodb

import (
	"context"
	"strings"
	"testing"
)

// TestDefaultAdminUser는 기본 admin user가 root 권한을 가진 admin DB 사용자로
// 구성되어야 함을 보장한다. 이 사양이 깨지면 클러스터 부트스트랩이 실패한다.
func TestDefaultAdminUser(t *testing.T) {
	u := DefaultAdminUser("p@ssw0rd!")
	if u.Username != "admin" {
		t.Errorf("Username = %q, want admin", u.Username)
	}
	if u.Database != "admin" {
		t.Errorf("Database = %q, want admin", u.Database)
	}
	if u.Password != "p@ssw0rd!" {
		t.Errorf("Password not propagated: got %q", u.Password)
	}
	if len(u.Roles) != 1 || u.Roles[0].Role != "root" || u.Roles[0].DB != "admin" {
		t.Errorf("expected single root@admin role, got %+v", u.Roles)
	}
}

// TestClusterAdminUser는 cluster admin이 운영에 필요한 3개 role(clusterAdmin,
// userAdminAnyDatabase, readWriteAnyDatabase)을 가져야 함을 검증.
func TestClusterAdminUser(t *testing.T) {
	u := ClusterAdminUser("ops", "secret")
	if u.Username != "ops" {
		t.Errorf("Username = %q, want ops", u.Username)
	}
	if u.Database != "admin" {
		t.Errorf("Database = %q, want admin", u.Database)
	}
	wantRoles := map[string]bool{"clusterAdmin": false, "userAdminAnyDatabase": false, "readWriteAnyDatabase": false}
	for _, r := range u.Roles {
		if r.DB != "admin" {
			t.Errorf("role %q: DB = %q, want admin", r.Role, r.DB)
		}
		if _, ok := wantRoles[r.Role]; ok {
			wantRoles[r.Role] = true
		}
	}
	for role, found := range wantRoles {
		if !found {
			t.Errorf("missing required role: %s", role)
		}
	}
}

// TestReadWriteUser는 특정 DB에 한정된 readWrite 사용자가 생성되는지 확인.
// AnyDatabase 권한이 새지 않아야 한다 (least-privilege 원칙).
func TestReadWriteUser(t *testing.T) {
	u := ReadWriteUser("app", "pw", "myapp")
	if u.Database != "myapp" {
		t.Errorf("Database = %q, want myapp", u.Database)
	}
	if len(u.Roles) != 1 {
		t.Fatalf("expected single role, got %d", len(u.Roles))
	}
	if u.Roles[0].Role != "readWrite" || u.Roles[0].DB != "myapp" {
		t.Errorf("expected readWrite@myapp, got %+v", u.Roles[0])
	}
}

// TestNewAuthManager_ReturnsDeprecated는 옛 생성자가 명시적 에러로 거부됨을 보장.
// Executor 시대 코드 경로가 다시 살아나면 이 테스트가 실패한다.
func TestNewAuthManager_ReturnsDeprecated(t *testing.T) {
	mgr, err := NewAuthManager()
	if err == nil {
		t.Fatal("expected deprecation error, got nil")
	}
	if mgr != nil {
		t.Errorf("expected nil manager, got %p", mgr)
	}
	if !strings.Contains(err.Error(), "deprecated") {
		t.Errorf("error should mention deprecation: %v", err)
	}
}

// TestNewAuthManagerWithFactory_NotNil은 권장 생성자가 매니저를 정상 반환하는지
// 확인. factory는 nil이어도 생성 자체는 성공해야 한다(driver 호출 시점에만 의미).
func TestNewAuthManagerWithFactory_NotNil(t *testing.T) {
	mgr := NewAuthManagerWithFactory(nil)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
}

// TestAuthManager_UserExists_ConnectError는 connect 실패가 명시적 label과 함께
// 전파되는지 검증. 디버깅 시 어떤 단계에서 실패했는지 추적 가능해야 한다.
func TestAuthManager_UserExists_ConnectError(t *testing.T) {
	mgr := NewAuthManagerWithFactory(failingFactory("dial fail"))
	exists, err := mgr.UserExists(context.Background(), "mongo-0", "ns", "admin", "admin")
	if err == nil {
		t.Fatal("expected error")
	}
	if exists {
		t.Error("expected exists=false on error")
	}
	if !strings.Contains(err.Error(), "connect for UserExists") {
		t.Errorf("missing label in error: %v", err)
	}
	if !strings.Contains(err.Error(), "dial fail") {
		t.Errorf("underlying error not wrapped: %v", err)
	}
}

// TestAuthManager_UserExistsInContainer_ConnectError는 InContainer wrapper도 동일
// 경로를 거치는지 확인.
func TestAuthManager_UserExistsInContainer_ConnectError(t *testing.T) {
	mgr := NewAuthManagerWithFactory(failingFactory("nx"))
	exists, err := mgr.UserExistsInContainer(context.Background(), "mongo-0", "ns", "mongodb", "admin", "admin", 27017)
	if err == nil {
		t.Fatal("expected error")
	}
	if exists {
		t.Error("expected exists=false")
	}
}

// TestAuthManager_CreateAdminUser_VerifiesViaUserExists는 CreateAdminUser가 더
// 이상 user를 "생성"하지 않고 UserExists로 검증한다는 새 의미론을 보장한다.
// connect가 실패하면 verify 실패로 wrap되어야 한다.
func TestAuthManager_CreateAdminUser_VerifiesViaUserExists(t *testing.T) {
	mgr := NewAuthManagerWithFactory(failingFactory("nx"))
	err := mgr.CreateAdminUser(context.Background(), "mongo-0", "ns", "admin", "pw")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "verify admin user") {
		t.Errorf("expected verify-label, got: %v", err)
	}
}

// TestAuthManager_CreateAdminUserInContainer_DelegatesToVerify는 InContainer
// 변형도 동일 verify 의미론을 따르는지 확인.
func TestAuthManager_CreateAdminUserInContainer_DelegatesToVerify(t *testing.T) {
	mgr := NewAuthManagerWithFactory(failingFactory("x"))
	err := mgr.CreateAdminUserInContainer(context.Background(), "mongo-0", "ns", "mongodb", "admin", "pw", 27017)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "verify admin user") {
		t.Errorf("expected verify-label, got: %v", err)
	}
}
