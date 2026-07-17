/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package resources

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// pitAt 은 RFC3339 문자열을 *metav1.Time 으로. 파싱 실패는 테스트 버그.
func pitAt(t *testing.T, rfc3339 string) *metav1.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, rfc3339)
	require.NoError(t, err)
	return &metav1.Time{Time: parsed}
}

func strPtr(s string) *string { return &s }

// TestOplogLimitArg 는 PIT → --oplogLimit 변환의 경계를 못 박는다.
//
// 핵심은 "1752710400" 이 아니라 **+1** 이라는 점이다 — --oplogLimit 은 배타라
// (newer than or equal 을 버린다) 지정한 초를 포함하려면 다음 초의 0 번을
// 상한으로 줘야 한다. 이 한 칸이 어긋나면 "그 초에 일어난 write 를 통째로
// 잃거나 살리는" 조용한 오복원이 된다.
func TestOplogLimitArg(t *testing.T) {
	tests := []struct {
		name  string
		pit   *metav1.Time
		pitTs *string
		want  string
		// wantErrContains 가 비어 있지 않으면 에러를 기대한다.
		wantErrContains string
	}{
		{
			name: "둘 다 nil → 빈 문자열 (base 시점 복원, 에러 아님)",
			want: "",
		},
		{
			name: "RFC3339 만 → epoch+1:0 (그 초를 *포함*)",
			pit:  pitAt(t, "2025-07-17T00:00:00Z"),
			want: "1752710401:0",
		},
		{
			name:  "PointInTimeTimestamp 가 PointInTime 을 이긴다",
			pit:   pitAt(t, "2025-07-17T00:00:00Z"),
			pitTs: strPtr("1752710400:7"),
			want:  "1752710400:7",
		},
		{
			name:  "PointInTimeTimestamp 단독 → 그대로 (ordinal 보존)",
			pitTs: strPtr("1752710400:42"),
			want:  "1752710400:42",
		},
		{
			name:  "빈 PointInTimeTimestamp 는 미설정 취급 → PointInTime fallback",
			pit:   pitAt(t, "2025-07-17T00:00:00Z"),
			pitTs: strPtr(""),
			want:  "1752710401:0",
		},
		{
			name:  "빈 PointInTimeTimestamp + PointInTime 도 nil → 빈 문자열",
			pitTs: strPtr(""),
			want:  "",
		},
		{
			name: "나노초는 초로 내림 — 결과는 같은 초 포함",
			pit:  &metav1.Time{Time: time.Unix(1752710400, 999999999).UTC()},
			want: "1752710401:0",
		},
		{
			name:  "0 채움 입력은 정규화 (8진수 오해석 차단)",
			pitTs: strPtr("0010:008"),
			want:  "10:8",
		},
		{
			name:  "ordinal 0 명시도 유효 (그 초 전체 배제 의미)",
			pitTs: strPtr("1752710400:0"),
			want:  "1752710400:0",
		},
		// ── 에러 경로 ────────────────────────────────────────────────────────
		{
			name:            "콜론 없음 → 에러",
			pitTs:           strPtr("1752710400"),
			wantErrContains: `"<sec>:<ordinal>"`,
		},
		{
			name:            "ordinal 비숫자 → 에러",
			pitTs:           strPtr("1752710400:abc"),
			wantErrContains: "ordinal",
		},
		{
			name:            "sec 비숫자 → 에러",
			pitTs:           strPtr("abc:1"),
			wantErrContains: "sec",
		},
		{
			name:            "콜론 3개 → 에러 (ordinal 이 uint32 가 아님)",
			pitTs:           strPtr("1:2:3"),
			wantErrContains: "ordinal",
		},
		{
			name:            "음수 부호 → 에러 (uint32 아님)",
			pitTs:           strPtr("-1:0"),
			wantErrContains: "sec",
		},
		{
			name:            "sec uint32 초과 → 에러",
			pitTs:           strPtr("4294967296:0"),
			wantErrContains: "sec",
		},
		{
			name:            "ordinal uint32 초과 → 에러",
			pitTs:           strPtr("1:4294967296"),
			wantErrContains: "ordinal",
		},
		{
			name:            "zero-value PointInTime → 에러 (BSON ts 범위 밖 — 조용한 오복원 차단)",
			pit:             &metav1.Time{},
			wantErrContains: "BSON timestamp",
		},
		{
			name:            "epoch 이전 PointInTime → 에러",
			pit:             &metav1.Time{Time: time.Unix(-5, 0).UTC()},
			wantErrContains: "BSON timestamp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := OplogLimitArg(tt.pit, tt.pitTs)
			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				assert.Empty(t, got, "에러 시 인자는 비어야 한다 (부분값 사용 차단)")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestOplogLimitArg_InclusiveSecondSemantics 는 e2e acceptance 시나리오를
// 함수 수준으로 고정한다.
//
//	base → doc-A insert(t1) → 3s → doc-B insert(t1+3) → PIT=t1+1 로 복원
//	  ⇒ doc-A 존재 ∧ doc-B 부재
//
// limit 은 배타이므로 "doc-A 의 ts < limit <= doc-B 의 ts" 여야 한다.
func TestOplogLimitArg_InclusiveSecondSemantics(t *testing.T) {
	t1 := int64(1752710400) // doc-A 가 쓰인 초
	pit := &metav1.Time{Time: time.Unix(t1+1, 0).UTC()}

	got, err := OplogLimitArg(pit, nil)
	require.NoError(t, err)

	// PIT(t1+1) 을 *포함* → 상한은 t1+2 의 0 번.
	assert.Equal(t, "1752710402:0", got)

	// doc-A(t1) 는 limit 미만 → 적용됨. doc-B(t1+3) 는 limit 이상 → 버려짐.
	limitSec, _, err := parseBSONTimestamp(got)
	require.NoError(t, err)
	assert.Less(t, uint64(t1), limitSec, "doc-A 의 초는 limit 미만이어야 적용된다")
	assert.LessOrEqual(t, limitSec, uint64(t1+3), "doc-B 의 초는 limit 이상이어야 버려진다")
}
