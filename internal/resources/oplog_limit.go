/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// oplog_limit.go — PITR 복구 목표 시점 → mongorestore --oplogLimit 인자 변환.
//
// 구 구현은 이 계산을 restore Job 의 bash 안에서 했다:
//
//	EPOCH=$(date -u -d "${POINT_IN_TIME}" +%s)
//	--oplogLimit=${EPOCH}:0
//
// 문제 3 가지:
//  1. `date -d` 는 GNU 확장이라 이미지가 바뀌면 조용히 깨진다.
//  2. ordinal 0 고정 — 사용자가 초 내 순번까지 지정해도 버려진다.
//  3. epoch 를 그대로 쓰면 그 초 전체가 잘린다 (아래 "경계 의미론" 참조).
//
// 순수 함수로 뽑아 table-driven test 로 경계를 못 박는다. bash 는 이제 이미
// 계산된 문자열을 받기만 한다.

package resources

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// bsonTimestampMax 는 BSON Timestamp 의 두 필드(sec / ordinal) 상한.
// 둘 다 uint32 다 — 넘는 값은 timestamp 로 표현할 수 없다.
const bsonTimestampMax = math.MaxUint32

// OplogLimitArg 는 RestoreSpec 의 복구 목표를 mongorestore `--oplogLimit` 인자
// ("<sec>:<ordinal>") 로 변환한다. 목표가 없으면 ""(빈 문자열) + nil error —
// 호출자는 이때 oplog replay 없이 base 스냅샷 시점 복원만 한다.
//
// # 우선순위
//
// pitTs(RestoreSpec.PointInTimeTimestamp)가 설정되면 그쪽이 이긴다. 초 내
// 순번까지 정확히 끊어야 할 때(특정 오작동 write 직전 등) 쓰는 탈출구다.
// 값은 정규화만 하고("007:8" → "7:8") ordinal 은 그대로 보존한다.
//
// # 경계 의미론 (중요)
//
// mongorestore 의 --oplogLimit 은 **배타**다 — 문서 표현으로 "prevents
// mongorestore from applying oplog entries newer than or equal to the
// timestamp". 즉 limit 과 같은 ts 의 op 부터는 적용되지 않는다.
//
// 반면 pit(PointInTime)은 RFC3339 **초 단위**라 그 초 안의 순번을 지목할 수
// 없다. 그래서 "그 초를 포함" 과 "그 초를 배제" 중 하나를 골라야 한다:
//
//	포함 (택함): limit = <epoch+1>:0  → 초 T 의 op 를 전부 적용
//	배제        : limit = <epoch>:0    → 초 T 의 op 를 전부 버림
//
// **포함**을 택한다. "T 시점으로 복원" 의 일반적 의미는 "T 시점 현재의 상태"
// (PBM / MongoDB 문서의 point-in-time 관행)이고, 배제 의미가 필요하면
// pitTs 로 경계를 직접 찍으면 되기 때문이다. epoch+1:0 은 초 T+1 의 첫
// op(ordinal 은 1 부터 시작)보다 앞서므로 초 T 까지만 정확히 포함한다.
//
// pit 이 나노초를 갖더라도 Unix() 가 초로 내림하므로 결과는 동일하다
// (= "그 초 전체 포함").
func OplogLimitArg(pit *metav1.Time, pitTs *string) (string, error) {
	// pitTs 우선 — 단 빈 문자열은 미설정으로 본다(CRD 패턴상 API 경유로는
	// 도달 불가하나, 다른 호출 경로에서 &"" 가 올 수 있다).
	if pitTs != nil && *pitTs != "" {
		sec, ord, err := parseBSONTimestamp(*pitTs)
		if err != nil {
			return "", fmt.Errorf("pointInTimeTimestamp %q: %w", *pitTs, err)
		}
		return fmt.Sprintf("%d:%d", sec, ord), nil
	}

	if pit == nil {
		return "", nil
	}

	// 경계 의미론 참조 — 지정한 초를 *포함*하려면 다음 초의 0 번을 배타 상한으로.
	sec := pit.Unix() + 1
	if sec <= 0 || sec > bsonTimestampMax {
		return "", fmt.Errorf(
			"pointInTime %s: BSON timestamp 로 표현할 수 없다 (epoch+1=%d, 유효 범위 1~%d)",
			pit.UTC().Format(time.RFC3339), sec, int64(bsonTimestampMax))
	}
	return fmt.Sprintf("%d:0", sec), nil
}

// parseBSONTimestamp 는 "<sec>:<ordinal>" 을 파싱한다. 두 필드 모두 uint32
// 여야 한다 (BSON Timestamp 의 실제 표현). CRD 의 Pattern 검증과 중복이지만
// 본 함수는 API 를 거치지 않는 호출(controller 내부 / 테스트)에도 쓰이므로
// 자체 방어한다.
func parseBSONTimestamp(s string) (uint64, uint64, error) {
	secStr, ordStr, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, fmt.Errorf(`형식은 "<sec>:<ordinal>" 이어야 한다 (예: "1752710400:7")`)
	}
	sec, err := strconv.ParseUint(secStr, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("sec %q 가 uint32 가 아니다", secStr)
	}
	ord, err := strconv.ParseUint(ordStr, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("ordinal %q 가 uint32 가 아니다", ordStr)
	}
	return sec, ord, nil
}
