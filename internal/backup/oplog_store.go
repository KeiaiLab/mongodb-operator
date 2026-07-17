/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// Package backup 는 controller.OplogSegmentStore 의 실 구현 (minio-go 백엔드) 을
// 담는다.
//
// # 왜 별도 패키지인가
//
// OplogSegmentStore 인터페이스와 순수 도메인 로직 (키 파싱 / window / GC plan) 은
// internal/controller 에 있다. 그 구현체를 controller 패키지 안에 두면 operator
// 바이너리가 항상 S3 SDK 를 링크하게 되고, 무엇보다 controller 테스트가 minio
// 의존을 끌고 오게 된다. 접합만 별도 패키지로 떼어 두면 controller → backup 의
// 단방향 의존이 없어 순환이 원천 차단되고 (backup → controller 만 존재), 배선은
// cmd/main.go 한 곳에서만 일어난다.
//
// # Ceph RGW (S3 호환) 대응
//
// 클러스터 오브젝트 스토어는 Ceph RGW 라 endpoint 가 커스텀이다. 이때
// virtual-host 스타일 (bucket.host) 은 RGW 에 와일드카드 DNS 설정을 요구하므로,
// 커스텀 endpoint 에는 항상 path-style (host/bucket) 을 강제한다 (resolveEndpoint).
// 실 AWS (endpoint 미지정) 만 자동 판정에 맡긴다.
package backup

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/controller"
)

const (
	// s3AccessKeyField / s3SecretKeyField — 자격 Secret 의 키. shell 3형제
	// (oplog-stream / backup-s3 / restore-fetch) 및 builder_backup.go 의
	// buildS3EnvVars 와 동일한 계약이라 반드시 일치해야 한다.
	s3AccessKeyField = "access-key"
	s3SecretKeyField = "secret-key"

	// defaultS3Endpoint — S3StorageSpec.Endpoint 가 비면 실 AWS 로 본다.
	defaultS3Endpoint = "s3.amazonaws.com"
)

// s3Credentials 는 자격 Secret 에서 뽑은 접근 키 쌍.
type s3Credentials struct {
	accessKey string
	secretKey string
}

// objectBackend 는 Store 가 필요로 하는 *최소* 오브젝트 스토어 연산이다. minio
// 는 실 서버 없이는 못 돌리므로, 이 seam 을 두어 테스트가 network 0 으로
// list/remove 흐름 (특히 계약 위반 키 skip) 을 검증한다. 실제 구현은
// minioBackend.
type objectBackend interface {
	// listKeys 는 prefix 밑의 모든 객체 키를 (재귀) 나열한다.
	listKeys(ctx context.Context, bucket, prefix string) ([]string, error)
	// removeKeys 는 주어진 키들을 삭제하고, 실패한 키를 묶어 에러로 보고한다.
	removeKeys(ctx context.Context, bucket string, keys []string) error
	// getObject 는 단일 키의 내용을 통째로 읽는다. 키가 없으면 errObjectNotFound
	// 를 반환한다 (호출자가 "없음" 을 정상 흐름으로 구분할 수 있게). base.meta.json
	// 같은 작은 메타 객체 전용 — 대용량 스트리밍 용도가 아니다.
	getObject(ctx context.Context, bucket, key string) ([]byte, error)
}

// errObjectNotFound 는 getObject 대상 키가 존재하지 않을 때의 sentinel.
// (base.meta.json 부재 = --oplog 없이 뜬 백업 = 정상 흐름이라 에러 아님.)
var errObjectNotFound = errors.New("object not found")

// S3SegmentStore 는 controller.OplogSegmentStore 의 minio-go 구현이다.
// 자격 Secret 은 CredentialsRef 가 가리키는 namespace 안에서 k8s client 로 읽고,
// minio client 는 호출마다 lazily 만든다 (배선 시점 실패 없음 = operator 기동은
// 항상 계속되고, 자격/엔드포인트 오류는 reconcile 에러로 표면화돼 requeue 된다).
type S3SegmentStore struct {
	// kube 는 자격 Secret 을 읽는 k8s 클라이언트. (import 된 client 패키지명과
	// 겹치지 않도록 field 명은 kube — revive import-shadowing 회피.)
	kube client.Client

	// newBackend 는 테스트 seam. nil 이면 minioBackend 를 만든다.
	newBackend func(s3 *mongodbv1alpha1.S3StorageSpec, creds s3Credentials) (objectBackend, error)
}

// 컴파일 타임 계약 — 인터페이스 시그니처가 어긋나면 여기서 빌드가 깨진다.
var _ controller.OplogSegmentStore = (*S3SegmentStore)(nil)

// NewS3SegmentStore 는 mgr client 로 배선되는 기본 생성자. 에러를 반환하지
// 않는다 — S3 접속은 호출 시점에야 일어나므로 여기서 실패할 여지가 없다.
func NewS3SegmentStore(kube client.Client) *S3SegmentStore {
	return &S3SegmentStore{kube: kube}
}

// ListSegments 는 OplogSegmentPrefix 밑의 객체를 나열해 파싱 가능한 segment 만
// 반환한다. 계약 위반 키 (다른 확장자 / 토큰 깨짐 / start>end) 는 에러가 아니라
// skip 이다 (controller.ParseOplogSegmentKey godoc).
func (s *S3SegmentStore) ListSegments(ctx context.Context, s3 *mongodbv1alpha1.S3StorageSpec, namespace, clusterName string) ([]controller.OplogSegment, error) {
	backend, err := s.backendFor(ctx, s3, namespace)
	if err != nil {
		return nil, err
	}
	prefix := controller.OplogSegmentPrefix(s3.Prefix, clusterName)
	keys, err := backend.listKeys(ctx, s3.Bucket, prefix)
	if err != nil {
		return nil, fmt.Errorf("list oplog objects under %q: %w", prefix, err)
	}
	segments := make([]controller.OplogSegment, 0, len(keys))
	for _, key := range keys {
		if seg, ok := controller.ParseOplogSegmentKey(key); ok {
			segments = append(segments, seg)
		}
		// else: prefix 밑에 segment 아닌 키 = tailer 측 계약 위반. 조용히
		// skip 하되 window 계산에서 배제한다 (전체 실패로 올리면 위반 키 하나가
		// 복원 관측을 통째로 마비시킨다).
	}
	return segments, nil
}

// DeleteSegments 는 주어진 키들을 삭제한다. 빈 목록이면 S3 를 건드리지 않는다.
func (s *S3SegmentStore) DeleteSegments(ctx context.Context, s3 *mongodbv1alpha1.S3StorageSpec, namespace string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	backend, err := s.backendFor(ctx, s3, namespace)
	if err != nil {
		return err
	}
	if err := backend.removeKeys(ctx, s3.Bucket, keys); err != nil {
		return fmt.Errorf("remove oplog segments: %w", err)
	}
	return nil
}

// ReadBaseOplogEnd 는 base.meta.json 의 oplogEnd 를 읽어 base 스냅샷의 oplog
// 일관 시점(= replay 하한 / 유효 PIT 하한)을 초 정밀도 metav1.Time 으로 반환한다.
// base.meta.json 이 없으면 (= --oplog 없이 뜬 백업) (nil, nil) — 이는 정상이며
// 그 백업은 PITR 기점이 될 수 없을 뿐이다.
func (s *S3SegmentStore) ReadBaseOplogEnd(ctx context.Context, s3 *mongodbv1alpha1.S3StorageSpec, namespace, backupName string) (*metav1.Time, error) {
	backend, err := s.backendFor(ctx, s3, namespace)
	if err != nil {
		return nil, err
	}
	key := controller.BaseMetaKey(s3.Prefix, backupName)
	data, err := backend.getObject(ctx, s3.Bucket, key)
	if err != nil {
		if errors.Is(err, errObjectNotFound) {
			return nil, nil // --oplog 없이 뜬 base — PITR 기점 아님
		}
		return nil, fmt.Errorf("read base meta %q: %w", key, err)
	}
	var meta struct {
		OplogEnd string `json:"oplogEnd"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse base meta %q: %w", key, err)
	}
	sec, ok := parseOplogEndSeconds(meta.OplogEnd)
	if !ok {
		return nil, fmt.Errorf("base meta %q has malformed oplogEnd %q", key, meta.OplogEnd)
	}
	t := metav1.NewTime(time.Unix(sec, 0).UTC())
	return &t, nil
}

// parseOplogEndSeconds 는 base.meta.json 의 oplogEnd("<sec>:<inc>") 에서 초를
// 뽑는다. status.OplogStart 는 초 정밀도(metav1.Time)라 Ordinal 은 버린다 —
// replay 경계는 restore init container 가 아카이브 안 oplog 로 재계산한다.
func parseOplogEndSeconds(tok string) (int64, bool) {
	sep := strings.IndexByte(tok, ':')
	if sep <= 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(tok[:sep], 10, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// backendFor 는 자격을 해석하고 objectBackend 를 만든다 (테스트 seam 경유).
func (s *S3SegmentStore) backendFor(ctx context.Context, s3 *mongodbv1alpha1.S3StorageSpec, namespace string) (objectBackend, error) {
	creds, err := s.resolveCredentials(ctx, s3, namespace)
	if err != nil {
		return nil, err
	}
	if s.newBackend != nil {
		return s.newBackend(s3, creds)
	}
	backend, err := newMinioBackend(s3, creds)
	if err != nil {
		return nil, fmt.Errorf("init s3 client: %w", err)
	}
	return backend, nil
}

// resolveCredentials 는 CredentialsRef Secret 에서 access-key / secret-key 를
// 읽는다. env 주입 경로 (buildS3EnvVars) 와 달리 값을 직접 서명에 쓰므로,
// 키 부재를 명시적 에러로 올려 "빈 자격으로 조용히 401" 을 막는다.
func (s *S3SegmentStore) resolveCredentials(ctx context.Context, s3 *mongodbv1alpha1.S3StorageSpec, namespace string) (s3Credentials, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: namespace, Name: s3.CredentialsRef.Name}
	if err := s.kube.Get(ctx, key, secret); err != nil {
		return s3Credentials{}, fmt.Errorf("get s3 credentials secret %s: %w", key, err)
	}
	access := secret.Data[s3AccessKeyField]
	sec := secret.Data[s3SecretKeyField]
	if len(access) == 0 || len(sec) == 0 {
		return s3Credentials{}, fmt.Errorf("s3 credentials secret %s missing %q or %q", key, s3AccessKeyField, s3SecretKeyField)
	}
	return s3Credentials{accessKey: string(access), secretKey: string(sec)}, nil
}

// minioBackend 는 objectBackend 의 실 minio-go 구현.
type minioBackend struct {
	mc     *minio.Client
	bucket string
}

// newMinioBackend 는 spec + 자격에서 minio client 를 만든다.
func newMinioBackend(s3 *mongodbv1alpha1.S3StorageSpec, creds s3Credentials) (objectBackend, error) {
	host, secure, pathStyle := resolveEndpoint(s3)
	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(creds.accessKey, creds.secretKey, ""),
		Secure: secure,
	}
	if s3.Region != "" {
		opts.Region = s3.Region
	}
	if pathStyle {
		// Ceph RGW 등 커스텀 endpoint — path-style 강제 (virtual-host 는
		// 와일드카드 DNS 요구, 파일 상단 주석 참조).
		opts.BucketLookup = minio.BucketLookupPath
	}
	if secure && s3.InsecureSkipTLS {
		// self-signed RGW 대비 — 사용자 opt-in (InsecureSkipTLS) 일 때만 검증 생략.
		tr, err := insecureTransport()
		if err != nil {
			return nil, err
		}
		opts.Transport = tr
	}
	mc, err := minio.New(host, opts)
	if err != nil {
		return nil, err
	}
	return &minioBackend{mc: mc, bucket: s3.Bucket}, nil
}

// resolveEndpoint 는 S3StorageSpec.Endpoint 를 minio 가 받는 (host, secure,
// pathStyle) 로 변환한다.
//
//   - endpoint 비었으면 실 AWS: s3.amazonaws.com / TLS / 자동 lookup.
//   - http:// → 평문. https:// (또는 스킴 없음) → TLS.
//   - 커스텀 endpoint 는 항상 path-style (Ceph RGW 대응).
func resolveEndpoint(s3 *mongodbv1alpha1.S3StorageSpec) (host string, secure, pathStyle bool) {
	ep := strings.TrimSpace(s3.Endpoint)
	if ep == "" {
		return defaultS3Endpoint, true, false
	}
	secure = true
	switch {
	case strings.HasPrefix(ep, "https://"):
		ep = strings.TrimPrefix(ep, "https://")
	case strings.HasPrefix(ep, "http://"):
		ep = strings.TrimPrefix(ep, "http://")
		secure = false
	}
	ep = strings.TrimSuffix(ep, "/")
	return ep, secure, true
}

// insecureTransport 는 minio 기본 transport 를 복제하되 TLS 검증만 끈다
// (평문 강등이 아니라 self-signed 인증서 허용).
func insecureTransport() (http.RoundTripper, error) {
	tr, err := minio.DefaultTransport(true)
	if err != nil {
		return nil, err
	}
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 — InsecureSkipTLS opt-in
	return tr, nil
}

// listKeys 는 prefix 밑의 객체 키를 재귀 나열한다. 채널 중간의 에러는 즉시 전파.
func (b *minioBackend) listKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	var keys []string
	for obj := range b.mc.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}

// removeKeys 는 키들을 채널로 흘려 RemoveObjects 로 일괄 삭제하고, 실패한 키를
// 모아 에러로 보고한다 (부분 실패는 무해 — pruneSegments godoc: victim 은 어떤
// 보존 window 에도 기여 못 하는 쓰레기라 삭제는 멱등).
func (b *minioBackend) removeKeys(ctx context.Context, bucket string, keys []string) error {
	objectsCh := make(chan minio.ObjectInfo)
	go func() {
		defer close(objectsCh)
		for _, key := range keys {
			select {
			case objectsCh <- minio.ObjectInfo{Key: key}:
			case <-ctx.Done():
				return
			}
		}
	}()

	var failures []string
	for res := range b.mc.RemoveObjects(ctx, bucket, objectsCh, minio.RemoveObjectsOptions{}) {
		if res.Err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", res.ObjectName, res.Err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d object(s) failed: %s", len(failures), strings.Join(failures, "; "))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// getObject 는 단일 키를 통째로 읽는다. 키 부재는 errObjectNotFound 로 정규화한다
// (minio 는 NoSuchKey 를 GetObject 가 아니라 첫 Read 시점에 낸다).
func (b *minioBackend) getObject(ctx context.Context, bucket, key string) ([]byte, error) {
	obj, err := b.mc.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = obj.Close() }()
	data, err := io.ReadAll(obj)
	if err != nil {
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.Code == "NoSuchKey" {
			return nil, errObjectNotFound
		}
		return nil, err
	}
	return data, nil
}
