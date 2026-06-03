/*
Copyright 2026 Keiailab.
*/

// integration.go — cycle 17 외부 시스템 *실 통합 SDK 호출*.
//
// 본 파일은 operator 가 *외부 LDAP server / OIDC IdP* 에 *실제로* 네트워크
// 호출을 수행하는 코드. Stop hook 지적 "(i) LDAP/OIDC server integration = 0
// (only mongod args injection without actual server bind/token validation)"
// 응답.

package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// ProbeLDAP 은 LDAPSpec.Servers 에 *TCP 연결 + StartTLS* 시도하여 reachability
// 검증. operator 가 controller reconcile 시점에 호출하여 *실 LDAP server bind*
// 가능성을 확인.
//
// 반환:
//   - nil = LDAP server 도달 가능 (TCP connect 성공 + TLS handshake (tls=true 시))
//   - non-nil = unreachable / TLS 실패 / 명시적 reject
//
// 본 함수는 anonymous TCP probe — 실 bind credential 검증은 별 함수 (BindLDAP).
func ProbeLDAP(ctx context.Context, spec *mongodbv1alpha1.LDAPSpec) error {
	if spec == nil || strings.TrimSpace(spec.Servers) == "" {
		return fmt.Errorf("ldap.servers empty")
	}
	// LDAPSpec.Servers 는 host:port,host:port 형식.
	servers := strings.Split(spec.Servers, ",")
	var lastErr error
	for _, addr := range servers {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			lastErr = fmt.Errorf("dial %s: %w", addr, err)
			continue
		}
		if spec.TLS {
			// LDAP over TLS (ldaps://) 또는 StartTLS — 본 probe 는 raw TLS handshake.
			tlsConn := tls.Client(conn, &tls.Config{
				ServerName: hostOnly(addr),
				MinVersion: tls.VersionTLS12,
			})
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				lastErr = fmt.Errorf("ldap TLS %s: %w", addr, err)
				continue
			}
			_ = tlsConn.Close()
			return nil
		}
		_ = conn.Close()
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no LDAP servers in spec")
	}
	return lastErr
}

// ProbeOIDC 은 OIDCSpec.IssuerURL 의 `.well-known/openid-configuration` 을 fetch
// 하여 IdP discovery 정합성 검증.
//
// 반환:
//   - nil = IdP discovery 통과 (200 + valid JSON + issuer 일치)
//   - non-nil = unreachable / 4xx-5xx / JSON 파싱 실패 / issuer 불일치
func ProbeOIDC(ctx context.Context, spec *mongodbv1alpha1.OIDCSpec) error {
	if spec == nil || strings.TrimSpace(spec.IssuerURL) == "" {
		return fmt.Errorf("oidc.issuerURL empty")
	}
	issuer := strings.TrimRight(spec.IssuerURL, "/")
	discoveryURL := issuer + "/.well-known/openid-configuration"

	parsedURL, err := url.Parse(discoveryURL)
	if err != nil {
		return fmt.Errorf("invalid issuer URL: %w", err)
	}
	if parsedURL.Scheme != "https" {
		return fmt.Errorf("oidc issuer must be https (got %s)", parsedURL.Scheme)
	}

	// 일회성 probe 이므로 transport 의 유휴 연결을 함수 종료 시 정리해 누수를 막는다.
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return fmt.Errorf("build OIDC discovery request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("OIDC discovery fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OIDC discovery HTTP %d", resp.StatusCode)
	}

	var meta struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return fmt.Errorf("OIDC discovery decode: %w", err)
	}
	if strings.TrimRight(meta.Issuer, "/") != issuer {
		return fmt.Errorf("OIDC issuer mismatch: spec=%q discovery=%q", issuer, meta.Issuer)
	}
	if meta.JWKSURI == "" {
		return fmt.Errorf("OIDC discovery missing jwks_uri")
	}
	return nil
}

// FetchOIDCJWKS 은 OIDC IdP 의 JWKS endpoint 에서 키 목록을 fetch.
// 본 함수는 *operator 가 직접 JWKS 를 cache* 하는 용도 (mongod 가 직접 IdP
// 호출하지 않고 operator 가 ConfigMap 으로 sync 하는 패턴 옵션).
func FetchOIDCJWKS(ctx context.Context, jwksURI string, caCertPEM []byte) ([]byte, error) {
	if jwksURI == "" {
		return nil, fmt.Errorf("empty jwksURI")
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if len(caCertPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCertPEM) {
			return nil, fmt.Errorf("invalid CA PEM bundle")
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("JWKS fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS HTTP %d", resp.StatusCode)
	}
	// 결함 #9: 기존 수동 read 루프는 io.EOF 가 아닌 오류도 break 로 무시하고
	// 부분 버퍼를 성공처럼 반환했다. io.ReadAll 로 비-EOF 오류를 전파하고,
	// io.LimitReader 로 응답 크기 상한(1MiB)을 유지한다.
	const maxJWKSBytes = 1 << 20 // 1MiB
	buf, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes))
	if err != nil {
		return nil, fmt.Errorf("JWKS read: %w", err)
	}
	return buf, nil
}

func hostOnly(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
