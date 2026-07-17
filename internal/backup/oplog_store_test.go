/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// oplog_store_test.go — minio-go 백엔드 Store 의 접합 로직 회귀 가드.
//
// 키 파싱 자체 (계약 위반 skip / zero-pad / start>end) 는 controller 패키지의
// ParseOplogSegmentKey 가 소유·검증하므로 여기서 중복하지 않는다. 본 테스트는
// Store 계층 고유의 책임만 본다: (a) list → parse-or-skip 접합, (b) 자격 Secret
// 해석·부재 에러, (c) delete 위임 + 빈 목록 단락, (d) endpoint 해석 (path-style).
// 전부 network 0 — minio 는 newBackend seam 으로 대체한다.

package backup

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/controller"
)

// fakeBackend 는 objectBackend 의 network 0 대역.
type fakeBackend struct {
	keys      []string
	listErr   error
	removed   []string
	removeErr error
}

func (f *fakeBackend) listKeys(_ context.Context, _, _ string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.keys, nil
}

func (f *fakeBackend) removeKeys(_ context.Context, _ string, keys []string) error {
	f.removed = append(f.removed, keys...)
	return f.removeErr
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	return s
}

func credsSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Data: map[string][]byte{
			s3AccessKeyField: []byte("AKIAEXAMPLE"),
			s3SecretKeyField: []byte("secretvalue"),
		},
	}
}

func s3Spec() *mongodbv1alpha1.S3StorageSpec {
	return &mongodbv1alpha1.S3StorageSpec{
		Bucket:         "backups",
		Prefix:         "pitr/",
		CredentialsRef: corev1.LocalObjectReference{Name: "s3-creds"},
	}
}

// newStore 는 fake k8s client + (선택) fake backend 로 배선한 Store.
func newStore(t *testing.T, backend objectBackend, objs ...client.Object) *S3SegmentStore {
	t.Helper()
	kube := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build()
	s := NewS3SegmentStore(kube)
	if backend != nil {
		s.newBackend = func(_ *mongodbv1alpha1.S3StorageSpec, _ s3Credentials) (objectBackend, error) {
			return backend, nil
		}
	}
	return s
}

func ts(sec, ord uint32) controller.BSONTimestamp {
	return controller.BSONTimestamp{Sec: sec, Ordinal: ord}
}

func TestListSegments_ParsesValidAndSkipsViolations(t *testing.T) {
	t.Parallel()
	spec := s3Spec()
	// tailer 가 쓴 것과 동일한 방식으로 유효 키를 만든다 (round-trip 계약).
	seg1 := controller.FormatOplogSegmentKey(spec.Prefix, "rs0", ts(100, 0), ts(200, 0))
	seg2 := controller.FormatOplogSegmentKey(spec.Prefix, "rs0", ts(200, 1), ts(300, 0))
	// start>end (이상치) — Parse 가 false 로 skip 해야 한다.
	reversed := controller.FormatOplogSegmentKey(spec.Prefix, "rs0", ts(300, 0), ts(100, 0))

	backend := &fakeBackend{keys: []string{
		seg1,
		"pitr/rs0/oplog/not-a-segment.txt",   // 확장자 위반
		"pitr/rs0/oplog/brokentoken.bson.gz", // 토큰 깨짐
		reversed,
		seg2,
	}}
	s := newStore(t, backend, credsSecret("default", "s3-creds"))

	got, err := s.ListSegments(context.Background(), spec, "default", "rs0")
	if err != nil {
		t.Fatalf("ListSegments err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("parsed %d segments, want 2: %+v", len(got), got)
	}
	if got[0].Start != ts(100, 0) || got[0].End != ts(200, 0) {
		t.Errorf("segment[0] range = [%s, %s], want [100:0, 200:0]", got[0].Start, got[0].End)
	}
	if got[1].Start != ts(200, 1) || got[1].End != ts(300, 0) {
		t.Errorf("segment[1] range = [%s, %s], want [200:1, 300:0]", got[1].Start, got[1].End)
	}
}

func TestListSegments_EmptyBucket(t *testing.T) {
	t.Parallel()
	s := newStore(t, &fakeBackend{keys: nil}, credsSecret("default", "s3-creds"))
	got, err := s.ListSegments(context.Background(), s3Spec(), "default", "rs0")
	if err != nil {
		t.Fatalf("ListSegments err = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 segments, got %d", len(got))
	}
}

func TestListSegments_SecretMissing(t *testing.T) {
	t.Parallel()
	// Secret 미배치 → 자격 해석 실패.
	s := newStore(t, &fakeBackend{}) // no secret object
	_, err := s.ListSegments(context.Background(), s3Spec(), "default", "rs0")
	if err == nil {
		t.Fatal("expected error when credentials secret is absent")
	}
}

func TestListSegments_SecretMissingKeys(t *testing.T) {
	t.Parallel()
	bad := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "s3-creds"},
		Data:       map[string][]byte{s3AccessKeyField: []byte("only-access")},
	}
	s := newStore(t, &fakeBackend{}, bad)
	_, err := s.ListSegments(context.Background(), s3Spec(), "default", "rs0")
	if err == nil {
		t.Fatal("expected error when secret-key is missing")
	}
}

func TestListSegments_BackendError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("s3 unreachable")
	s := newStore(t, &fakeBackend{listErr: sentinel}, credsSecret("default", "s3-creds"))
	_, err := s.ListSegments(context.Background(), s3Spec(), "default", "rs0")
	if !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel, got %v", err)
	}
}

func TestDeleteSegments_EmptyIsNoop(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{}
	// Secret 조차 없어도 빈 목록은 단락되어 성공해야 한다 (backendFor 미호출).
	s := newStore(t, backend)
	if err := s.DeleteSegments(context.Background(), s3Spec(), "default", nil); err != nil {
		t.Fatalf("empty DeleteSegments err = %v", err)
	}
	if len(backend.removed) != 0 {
		t.Fatalf("backend touched on empty keys: %v", backend.removed)
	}
}

func TestDeleteSegments_ForwardsKeys(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{}
	s := newStore(t, backend, credsSecret("default", "s3-creds"))
	keys := []string{"pitr/rs0/oplog/a.bson.gz", "pitr/rs0/oplog/b.bson.gz"}
	if err := s.DeleteSegments(context.Background(), s3Spec(), "default", keys); err != nil {
		t.Fatalf("DeleteSegments err = %v", err)
	}
	if len(backend.removed) != 2 || backend.removed[0] != keys[0] || backend.removed[1] != keys[1] {
		t.Fatalf("forwarded keys = %v, want %v", backend.removed, keys)
	}
}

func TestDeleteSegments_BackendError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("access denied")
	s := newStore(t, &fakeBackend{removeErr: sentinel}, credsSecret("default", "s3-creds"))
	err := s.DeleteSegments(context.Background(), s3Spec(), "default", []string{"pitr/rs0/oplog/a.bson.gz"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel, got %v", err)
	}
}

func TestResolveEndpoint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		endpoint      string
		wantHost      string
		wantSecure    bool
		wantPathStyle bool
	}{
		{"empty is aws", "", defaultS3Endpoint, true, false},
		{"bare host is tls path-style", "rgw.example.com", "rgw.example.com", true, true},
		{"https scheme", "https://rgw.example.com", "rgw.example.com", true, true},
		{"http scheme is plaintext", "http://rgw.example.com", "rgw.example.com", false, true},
		{"trailing slash trimmed", "https://rgw.example.com/", "rgw.example.com", true, true},
		{"host port plaintext", "http://10.0.0.1:8080", "10.0.0.1:8080", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host, secure, pathStyle := resolveEndpoint(&mongodbv1alpha1.S3StorageSpec{Endpoint: tc.endpoint})
			if host != tc.wantHost || secure != tc.wantSecure || pathStyle != tc.wantPathStyle {
				t.Errorf("resolveEndpoint(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tc.endpoint, host, secure, pathStyle, tc.wantHost, tc.wantSecure, tc.wantPathStyle)
			}
		})
	}
}
