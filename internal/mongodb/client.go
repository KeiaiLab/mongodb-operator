/*
Copyright 2024 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package mongodb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// adminUserDB 는 MongoDB 의 admin database 이자 root user 명. RS 부트스트랩,
// 인증, 권한 부여 등 다수의 호출자가 동일 문자열을 사용 — goconst 검출 차단 +
// 의미 일관 보장을 위한 single source of truth.
const adminUserDB = "admin"

// ConnectOpts는 mongo-go-driver client 생성에 필요한 파라미터를 모은다.
//
// 자격증명(Username/Password)은 URI 문자열에 직접 포함하지 않고
// options.Credential로 분리 전달한다. URI 인코딩 누락에 따른 인증 실패나
// 특수문자 인젝션을 차단하기 위함.
type ConnectOpts struct {
	// Hosts는 "<fqdn>:<port>" 형식의 호스트 목록. 빈 슬라이스면 에러.
	Hosts []string

	// Username, Password, AuthDB는 SCRAM-SHA-256 인증 정보.
	// Username이 비어있으면 인증 없이 연결한다 (부트스트랩 단계 등).
	Username string
	Password string
	AuthDB   string

	// ReplicaSet 이름이 지정되면 driver가 replica set discovery 모드로 동작한다.
	// Direct=true와 상호 배타.
	ReplicaSet string

	// Direct=true면 directConnection=true로 단일 호스트에 직접 연결한다.
	// 용도: replica set이 아직 초기화되지 않은 첫 멤버에 붙을 때
	// (rs.initiate 호출 전 단계). 이 모드에서는 ReplicaSet을 무시한다.
	Direct bool

	// Timeout은 connect/serverSelection의 상한. 0이면 기본 10초.
	Timeout time.Duration
}

// NewClient는 ConnectOpts를 기반으로 mongo.Client를 만들고 ping까지 검증한다.
// 호출자는 반드시 defer client.Disconnect(ctx)로 해제해야 한다.
//
// v2 API는 mongo.Connect(opts)에 context를 받지 않는다. 대신 Ping/Disconnect에
// context로 시간 제한을 거는 패턴이다.
func NewClient(ctx context.Context, opts ConnectOpts) (*mongo.Client, error) {
	if len(opts.Hosts) == 0 {
		return nil, fmt.Errorf("ConnectOpts.Hosts 비어있음")
	}
	if opts.Direct && opts.ReplicaSet != "" {
		return nil, fmt.Errorf("ConnectOpts.Direct=true와 ReplicaSet 동시 지정 불가")
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	uri := buildURI(opts.Hosts, opts.ReplicaSet, opts.Direct)

	clientOpts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(timeout).
		SetServerSelectionTimeout(timeout).
		SetRetryWrites(true).
		SetRetryReads(true)

	if opts.Username != "" {
		// AuthSource 누락 시 SCRAM이 default DB("test")를 인증 DB로 시도해 실패한다.
		// 호출자가 비워서 넘기는 실수를 차단하기 위해 명시적으로 adminUserDB 기본값을 보강.
		authDB := opts.AuthDB
		if authDB == "" {
			authDB = adminUserDB
		}
		// v2 driver에서 SetAuth는 receiver in-place mutate(*ClientOptions)이지만,
		// 반환값을 재할당해 두면 향후 driver가 immutable builder로 바뀌더라도 안전.
		// 또한 godoc/공식 예제가 권장하는 chaining 패턴과 일치.
		clientOpts = clientOpts.SetAuth(options.Credential{
			AuthMechanism: "SCRAM-SHA-256",
			AuthSource:    authDB,
			Username:      opts.Username,
			Password:      opts.Password,
		})
	}

	client, err := mongo.Connect(clientOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo.Connect 실패: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		// ping 실패 시 client는 즉시 닫는다. 호출자에게 부분 초기화 상태를 넘기지 않음.
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo Ping 실패: %w", err)
	}

	return client, nil
}

// NewClientFromURI는 이미 조립된 완전한 mongodb:// URI(자격증명 포함)로 client를
// 만들고 ping까지 검증한다. backup connectionString 처럼 secret에 저장된 URI를
// 그대로 사용하는 경로(백업 검증 등)에서 쓴다. 호출자는 defer client.Disconnect.
func NewClientFromURI(ctx context.Context, uri string, timeout time.Duration) (*mongo.Client, error) {
	if uri == "" {
		return nil, fmt.Errorf("mongodb URI 비어있음")
	}
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	clientOpts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(timeout).
		SetServerSelectionTimeout(timeout).
		SetRetryReads(true)

	client, err := mongo.Connect(clientOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo.Connect(URI) 실패: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo Ping 실패: %w", err)
	}

	return client, nil
}

// GetPodFQDN returns the fully qualified domain name for a pod within a
// StatefulSet headless service.
func GetPodFQDN(podName, serviceName, namespace string, port int) string {
	return fmt.Sprintf("%s.%s.%s.svc.cluster.local:%d", podName, serviceName, namespace, port)
}

// GetPodsFQDN returns FQDNs for multiple pods (statefulset ordinal pattern).
func GetPodsFQDN(baseName, serviceName, namespace string, replicas int, port int) []string {
	fqdns := make([]string, replicas)
	for i := 0; i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", baseName, i)
		fqdns[i] = GetPodFQDN(podName, serviceName, namespace, port)
	}
	return fqdns
}

// buildURI는 ConnectOpts에서 자격증명을 제외한 mongodb:// URI를 만든다.
// 자격증명은 호출자가 options.Credential로 별도 전달하므로 URI에는 포함하지 않는다.
func buildURI(hosts []string, replicaSet string, direct bool) string {
	var b strings.Builder
	b.WriteString("mongodb://")
	b.WriteString(strings.Join(hosts, ","))

	params := []string{}
	if direct {
		params = append(params, "directConnection=true")
	} else if replicaSet != "" {
		params = append(params, "replicaSet="+replicaSet)
	}
	if len(params) > 0 {
		b.WriteString("/?")
		b.WriteString(strings.Join(params, "&"))
	}

	return b.String()
}
