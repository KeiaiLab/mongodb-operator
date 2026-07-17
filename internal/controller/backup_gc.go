/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// backup_gc.go — PITR 복원 가능 window 계산 + retention GC 의 *순수 도메인 로직*.
//
// 본 파일은 k8s client 도 S3 client 도 모른다. 입력 (아카이브된 oplog segment
// 목록 + base 백업 메타 + retention 정책) 에서 출력 (window / 삭제 plan) 만
// 계산하는 부수효과 없는 함수 모음이라, oplog_uploader_test.go 가 네트워크 0 /
// API 서버 0 으로 전수 검증한다. 실제 조회·삭제 (k8s API + S3) 는 본 파일이
// 아니라 oplog_uploader.go 가 담당한다.
//
// # 아키텍처 전제 (A. 사이드카 직접 스트리밍)
//
// oplog tailer 사이드카가 staging volume 을 경유하지 않고 증분 tail → S3 로
// 직접 스트리밍하며, **세그먼트 키 자체가 시간 범위를 담는다**:
//
//	<prefix><cluster>/oplog/<startTs>_<endTs>.bson.gz
//
// 덕분에 별도 상태 저장소 없이 S3 list 하나로 (a) tailer 의 resume token 과
// (b) 복원 가능 window 를 모두 유도한다. 본 파일은 (b) 담당.
//
// # ts 토큰 계약 (tailer 와 공유)
//
// startTs / endTs 는 `<sec>-<ordinal>` — 각각 10 자리 zero-pad 한 십진수라
// 고정폭이다 (BSON Timestamp 의 sec/ordinal 은 uint32 = 최대 10 자리). 고정폭
// 이므로 S3 의 사전식 list 순서가 곧 시간순이 되어 tailer 가 "마지막 키" 를
// O(1) 로 집을 수 있다. 다만 본 파일의 계산은 사전식 순서에 기대지 않고
// **파싱 후 수치 정렬**하므로, tailer 가 zero-pad 폭을 바꾸더라도 window 는
// 조용히 깨지지 않는다 (파서는 폭에 관대, 구조에는 엄격).
//
// 대응하는 shell 측 표기 (tailer):
//
//	printf '%010d-%010d_%010d-%010d.bson.gz' "$s1" "$o1" "$s2" "$o2"

package controller

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

const (
	// oplogKeyInfix — cluster 별 oplog segment 가 모이는 키 중간 경로.
	// 전체 prefix = <S3.Prefix><cluster>/oplog/ (OplogSegmentPrefix 참조).
	oplogKeyInfix = "oplog/"

	// oplogSegmentExt — segment 객체 확장자. mongodump --archive 산출물을
	// gzip 한 것이라 .bson.gz.
	oplogSegmentExt = ".bson.gz"

	// oplogTSSeparator — ts 토큰 안에서 sec 과 ordinal 을 가르는 문자.
	oplogTSSeparator = '-'

	// oplogRangeSeparator — 파일명에서 startTs 와 endTs 를 가르는 문자.
	oplogRangeSeparator = '_'

	// oplogTSPadWidth — ts 구성요소의 zero-pad 폭. uint32 최대값
	// (4294967295) 이 10 자리라 10 이면 항상 고정폭이 된다.
	oplogTSPadWidth = 10
)

// BSONTimestamp 는 mongod oplog 의 `ts` 필드 원형 (BSON Timestamp) 이다.
// Sec = unix epoch 초, Ordinal = 그 초 안의 연산 순번. mongorestore 의
// `--oplogLimit <sec>:<ordinal>` 과 1:1 대응한다.
//
// PITR replay 경계의 **진본**이다 — MongoDBBackupStatus 의 metav1.Time 필드들
// (OplogStart / EarliestRestore / LatestRestore) 은 RFC3339 초 단위라 Ordinal
// 을 담지 못하므로 window 계산·표시 전용이다 (api/v1alpha1 godoc 참조).
type BSONTimestamp struct {
	Sec     uint32
	Ordinal uint32
}

// Compare 는 t 가 o 보다 앞이면 <0, 같으면 0, 뒤면 >0 을 반환한다.
// oplog 순서 정의와 동일하게 Sec 우선, 동률이면 Ordinal 로 판정.
func (t BSONTimestamp) Compare(o BSONTimestamp) int {
	switch {
	case t.Sec != o.Sec:
		if t.Sec < o.Sec {
			return -1
		}
		return 1
	case t.Ordinal != o.Ordinal:
		if t.Ordinal < o.Ordinal {
			return -1
		}
		return 1
	default:
		return 0
	}
}

// String 은 mongorestore --oplogLimit 이 받는 "<sec>:<ordinal>" 표기를 반환.
func (t BSONTimestamp) String() string {
	return strconv.FormatUint(uint64(t.Sec), 10) + ":" + strconv.FormatUint(uint64(t.Ordinal), 10)
}

// Time 은 초 단위 UTC time.Time 으로 변환한다 (Ordinal 은 소실 — 표시·window
// 폭 계산 전용이며 replay 경계로 되돌려 쓰지 말 것).
func (t BSONTimestamp) Time() time.Time {
	return time.Unix(int64(t.Sec), 0).UTC()
}

// bsonTSFromTime 은 time.Time 을 초 정밀도 BSONTimestamp 로 변환한다
// (Ordinal=0). uint32 범위 밖은 clamp — 1970 이전 / 2106 이후는 oplog ts 로
// 표현 불가이므로 경계값으로 접는다.
func bsonTSFromTime(t time.Time) BSONTimestamp {
	sec := t.Unix()
	switch {
	case sec < 0:
		sec = 0
	case sec > math.MaxUint32:
		sec = math.MaxUint32
	}
	return BSONTimestamp{Sec: uint32(sec)}
}

// OplogSegment 는 S3 에 아카이브된 oplog segment 1건이다. [Start, End] 는
// 그 객체가 담고 있는 oplog 항목의 ts 범위 (양끝 포함).
type OplogSegment struct {
	// Key 는 bucket 안의 전체 객체 키.
	Key string
	// Start 는 segment 첫 oplog 항목의 ts.
	Start BSONTimestamp
	// End 는 segment 마지막 oplog 항목의 ts. 다음 segment 의 Start 와
	// 이어지는지가 chain 연속성 판정 기준이다.
	End BSONTimestamp
}

// OplogSegmentPrefix 는 주어진 cluster 의 oplog segment 가 모이는 S3 키
// prefix 를 반환한다.
//
// s3Prefix 는 S3StorageSpec.Prefix 를 그대로 받는다. 정규화 규약은 키를 *실제로
// 쓰는* 쪽인 shell 3형제와 반드시 동일해야 한다 (oplog-stream / backup-s3 /
// restore-fetch 전부 `PFX="${S3_PREFIX%/}"` 후 "/" 로 join):
//
//	prefix "pitr/" → "pitr/<cluster>/oplog/"
//	prefix "pitr"  → "pitr/<cluster>/oplog/"   ← 생짜 연결이면 "pitrrs0/..." 로 어긋난다
//	prefix ""      → "<cluster>/oplog/"
//
// 어긋나면 tailer 가 쓴 세그먼트를 uploader 가 영영 못 봐서 window 가 조용히
// 빈 채로 남고(복원 불가로 오인) GC 도 prune 대상을 못 찾는다.
func OplogSegmentPrefix(s3Prefix, clusterName string) string {
	pfx := strings.TrimSuffix(s3Prefix, "/")
	if pfx != "" {
		pfx += "/"
	}
	return pfx + clusterName + "/" + oplogKeyInfix
}

// BaseMetaKey 는 base.meta.json 의 전체 S3 키를 만든다. backup-s3.sh.tpl 의
// BASE_PREFIX(= "${S3_PREFIX%/}/${BACKUP_NAME}") 계약과 반드시 일치해야 한다.
// oplog segment(clusterName 기준)와 달리 base 는 *backup 이름* 아래 둔다 —
// restore 는 SourceBackupName 만 알고 소스 클러스터 이름은 모르기 때문이다.
func BaseMetaKey(s3Prefix, backupName string) string {
	pfx := strings.TrimSuffix(s3Prefix, "/")
	if pfx != "" {
		pfx += "/"
	}
	return pfx + backupName + "/base.meta.json"
}

// FormatOplogSegmentKey 는 [start, end] 를 담는 segment 의 전체 S3 키를 만든다.
// tailer 의 shell printf 와 동일한 결과여야 한다 (본 파일 상단 ts 토큰 계약).
func FormatOplogSegmentKey(s3Prefix, clusterName string, start, end BSONTimestamp) string {
	return OplogSegmentPrefix(s3Prefix, clusterName) +
		formatOplogTSToken(start) + string(oplogRangeSeparator) +
		formatOplogTSToken(end) + oplogSegmentExt
}

func formatOplogTSToken(t BSONTimestamp) string {
	return fmt.Sprintf("%0*d%c%0*d", oplogTSPadWidth, t.Sec, oplogTSSeparator, oplogTSPadWidth, t.Ordinal)
}

// ParseOplogSegmentKey 는 S3 키에서 segment 의 시간 범위를 복원한다.
// 계약에 맞지 않는 키 (다른 확장자 / 토큰 깨짐 / start>end) 는 ok=false —
// 호출자는 *에러가 아니라 skip* 으로 다루되 건수를 로그로 남긴다. oplog prefix
// 밑에는 segment 만 있어야 하므로 skip 이 생기면 tailer 측 계약 위반 신호다.
//
// zero-pad 폭에는 관대하다 (수치 파싱) — 폭이 바뀌어도 window 가 조용히
// 무너지지 않게 하기 위함. 구조 (확장자 / 구분자 / uint32 / start<=end) 에는
// 엄격하다.
func ParseOplogSegmentKey(key string) (OplogSegment, bool) {
	name := key
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if !strings.HasSuffix(name, oplogSegmentExt) {
		return OplogSegment{}, false
	}
	name = strings.TrimSuffix(name, oplogSegmentExt)

	sep := strings.IndexByte(name, oplogRangeSeparator)
	if sep < 0 {
		return OplogSegment{}, false
	}
	start, ok := parseOplogTSToken(name[:sep])
	if !ok {
		return OplogSegment{}, false
	}
	end, ok := parseOplogTSToken(name[sep+1:])
	if !ok {
		return OplogSegment{}, false
	}
	if start.Compare(end) > 0 {
		return OplogSegment{}, false
	}
	return OplogSegment{Key: key, Start: start, End: end}, true
}

func parseOplogTSToken(tok string) (BSONTimestamp, bool) {
	sep := strings.IndexByte(tok, oplogTSSeparator)
	if sep < 0 {
		return BSONTimestamp{}, false
	}
	sec, ok := parseUint32(tok[:sep])
	if !ok {
		return BSONTimestamp{}, false
	}
	ord, ok := parseUint32(tok[sep+1:])
	if !ok {
		return BSONTimestamp{}, false
	}
	return BSONTimestamp{Sec: sec, Ordinal: ord}, true
}

func parseUint32(s string) (uint32, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

// OplogWindow 는 base 스냅샷 1건 기준의 복원 가능 구간 판정 결과다.
type OplogWindow struct {
	// Base 는 base 스냅샷의 oplog 일관 시점 = window 하한 (= EarliestRestore).
	Base BSONTimestamp
	// Latest 는 Base 에서 출발해 끊김 없이 이어지는 segment chain 의 마지막
	// endTs = window 상한 (= LatestRestore). chain 이 없으면 Base 와 같다
	// (base 시점 복원만 가능).
	Latest BSONTimestamp
	// Covered 는 Base 직후를 덮는 segment chain 이 하나라도 있었는지.
	// false 면 Latest == Base.
	Covered bool
	// GapFrom / GapTo 는 chain 이 끊긴 첫 지점 — GapFrom 까지는 이어졌고
	// 다음 segment 가 GapTo 에서야 시작한다. gap 이 없으면 둘 다 nil.
	// (Covered=false + GapFrom!=nil = base 직후부터 비어 있다는 뜻.)
	GapFrom *BSONTimestamp
	GapTo   *BSONTimestamp
}

// Duration 은 window 의 폭. 초 정밀도 (Ordinal 무시).
func (w OplogWindow) Duration() time.Duration {
	return w.Latest.Time().Sub(w.Base.Time())
}

// ComputeOplogWindow 는 base 앵커와 segment 목록에서 복원 가능 window 를
// 계산한다.
//
// 알고리즘 — Start 기준 정렬 후 greedy 로 reach 를 전진시킨다:
//
//	reach := base
//	각 segment s 를 Start 오름차순으로 순회:
//	  End <= reach            → 이미 덮인 구간 (또는 base 이전) → skip
//	  Start > reach           → gap → 중단
//	  그 외 (Start <= reach)  → reach = End  (연속 / 중첩 → 연결)
//
// "endTs[n] >= startTs[n+1] 이면 연결" 규칙과 동치이며, 중첩·순서 뒤섞임·
// base 이전 잔재 segment 를 모두 올바로 흡수한다. O(n log n).
//
// # 정밀도 주의
//
// base 는 status.OplogStart 에서 오므로 초 단위로 절삭돼 있다 (Ordinal=0).
// 따라서 판정에 최대 1 초의 낙관 오차가 있다 — 그 1 초 안에서 chain 이
// 끊겼는데 이어졌다고 볼 여지. window 는 표시 + admission webhook 의 *권고*
// 게이트 (fail-open) 용이고 실제 replay 가부는 restore job 이 authoritative
// 하므로 (api/v1alpha1 RestoreSpec godoc) 이 오차는 설계상 수용한다.
func ComputeOplogWindow(base BSONTimestamp, segments []OplogSegment) OplogWindow {
	sorted := make([]OplogSegment, len(segments))
	copy(sorted, segments)
	sort.Slice(sorted, func(i, j int) bool {
		if c := sorted[i].Start.Compare(sorted[j].Start); c != 0 {
			return c < 0
		}
		return sorted[i].End.Compare(sorted[j].End) < 0
	})

	out := OplogWindow{Base: base, Latest: base}
	reach := base
	for _, s := range sorted {
		if s.End.Compare(reach) <= 0 {
			// base 이전이거나 이미 덮인 구간 — window 에 기여하지 않는다.
			continue
		}
		if s.Start.Compare(reach) > 0 {
			// reach 와 s.Start 사이가 비었다 = gap. 여기서 window 가 잘린다.
			from, to := reach, s.Start
			out.GapFrom, out.GapTo = &from, &to
			break
		}
		reach = s.End
		out.Covered = true
	}
	out.Latest = reach
	return out
}

// BaseBackup 은 retention 판정 + oplog floor 계산에 필요한 base 백업 메타의
// 최소 투영이다. MongoDBBackup CR → 본 구조체 변환은 oplog_uploader.go 담당
// (본 파일을 k8s 비의존으로 유지하기 위한 경계).
type BaseBackup struct {
	// Name 은 MongoDBBackup CR 이름.
	Name string
	// CreatedAt 은 나이 판정 기준 시각 — CompletionTime 이 있으면 그것,
	// 없으면 CR 의 creationTimestamp.
	CreatedAt time.Time
	// OplogStart 는 base 스냅샷의 oplog 일관 시점. nil 이면 `--oplog` 없이
	// 뜬 백업이라 PITR 기점이 될 수 없다 (retention 대상에는 포함).
	OplogStart *BSONTimestamp
	// Location 은 status.Location (base archive 위치). 현재 base archive 의
	// 실제 S3 키와 불일치하는 알려진 결함이 있어 GC 실행에는 쓰지 않는다
	// — PlanBackupRetention 의 판정 대상도 아니고, 진단 로그용으로만 보존.
	Location string
}

// BackupRetentionPlan 은 base 백업 보존/만료 판정 결과다.
type BackupRetentionPlan struct {
	// Retained 는 계속 보존할 백업 (신규 → 오래된 순).
	Retained []BaseBackup
	// Expired 는 정책상 만료된 백업 (신규 → 오래된 순).
	//
	// **주의 — 본 트랙은 Expired 를 실제로 삭제하지 않는다.** 만료 판정은
	// oplog GC 의 floor (= 가장 오래된 *보존* base 의 OplogStart) 를 구하기
	// 위해 존재한다. base archive 삭제를 실행하지 않는 이유는
	// OplogGCCutoff godoc 참조.
	Expired []BaseBackup
}

// PlanBackupRetention 은 retention 정책으로 base 백업의 보존/만료를 가른다.
//
// # 판정 규칙 — Days ∧ Count (둘 다 만족해야 만료)
//
// 어느 한 축만 걸려도 보존한다. 30 일 된 백업이라도 최신 Count 개 안에 들면
// 남기고, 51 번째 백업이라도 Days 안쪽이면 남긴다 — 보수적 = 안전.
//
//	Days 설정  Count 설정  판정
//	----------------------------------------------------------
//	  X          X        전부 보존 (정책 부재 = GC 안 함)
//	  O          X        나이만으로 판정
//	  X          O        개수만으로 판정
//	  O          O        나이 ∧ 개수 둘 다 만족해야 만료
//
// 미설정 축은 *공허하게 참* 으로 접는다 (그 축이 제약하지 않음). 다만 둘 다
// 미설정이면 공허참 ∧ 공허참 = 전부 만료가 되어버리므로 맨 앞에서 차단한다.
// Retention 자체가 nil 이거나 Days<=0 ∧ Count<=0 이면 아무것도 만료시키지
// 않는다.
func PlanBackupRetention(backups []BaseBackup, retention *mongodbv1alpha1.RetentionSpec, now time.Time) BackupRetentionPlan {
	sorted := make([]BaseBackup, len(backups))
	copy(sorted, backups)
	// 신규 → 오래된 순. 동률은 이름으로 안정 정렬 (판정 재현성).
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].Name < sorted[j].Name
	})

	plan := BackupRetentionPlan{}
	if retention == nil {
		plan.Retained = sorted
		return plan
	}
	ageSet := retention.Days > 0
	countSet := retention.Count != nil && *retention.Count > 0
	if !ageSet && !countSet {
		// 정책 부재 — 공허참 ∧ 공허참 = 전부 만료 를 차단하는 필수 가드.
		plan.Retained = sorted
		return plan
	}

	maxAge := time.Duration(retention.Days) * 24 * time.Hour
	for rank, b := range sorted {
		ageExpired := ageSet && now.Sub(b.CreatedAt) > maxAge
		countExpired := countSet && rank >= *retention.Count
		if (!ageSet || ageExpired) && (!countSet || countExpired) {
			plan.Expired = append(plan.Expired, b)
			continue
		}
		plan.Retained = append(plan.Retained, b)
	}
	return plan
}

// OplogGCCutoff 는 oplog segment 삭제의 하한 시각을 구한다. 이 시각보다
// **완전히 이전**인 segment 만 삭제 대상이 된다.
//
// # 불변식 — oplog 는 base 보다 먼저 죽을 수 없다
//
//	cutoff = min(now - OplogRetentionHours, min(보존 base 의 OplogStart))
//
// 시간 기반 retention 단독으로 자르면 아직 유효한 base 의 replay 하한을
// 잘라먹어 그 base 가 base 시점 복원 전용으로 전락한다 (window 침범). 그래서
// 두 하한 중 **더 이른 쪽**을 택해 "retention 시간" 과 "가장 오래된 보존 base
// 커버" 중 넓은 쪽을 보존한다 (= 요구된 max(retentionHours, 최고령 base 커버)).
//
// ok=false 면 GC 를 건너뛴다 — retentionHours 가 0 이하 (정책 미설정) 인 경우.
// 보존 base 가 하나도 없거나 전부 OplogStart 가 없으면 base floor 제약이
// 없으므로 시간 기반 cutoff 만 적용한다.
//
// # base archive 는 왜 삭제하지 않는가
//
// base 백업의 실제 S3 키는 현재 CR 로부터 유도할 수 없다 — 백업 스크립트가
// `${CLUSTER}-$(date ...)` 로 컨테이너 안에서 이름을 짓는 반면 status.Location
// 은 CR 이름 기준으로 기록돼 서로 어긋난다 (알려진 결함). 이 상태에서 base
// 삭제를 실행하면 (a) 엉뚱한 키를 지우거나 (b) CR 만 지워 S3 객체를 고아로
// 남긴다 — 둘 다 "S3 무한 증가" 를 못 고치면서 비가역 손실 위험만 얹는다.
// 따라서 본 트랙은 base 만료를 *판정만* 하고 (oplog floor 계산에 필요),
// 삭제는 키 계약이 바로잡힌 뒤 후속으로 붙인다.
func OplogGCCutoff(retained []BaseBackup, oplogRetentionHours int, now time.Time) (BSONTimestamp, bool) {
	if oplogRetentionHours <= 0 {
		return BSONTimestamp{}, false
	}
	cutoff := bsonTSFromTime(now.Add(-time.Duration(oplogRetentionHours) * time.Hour))
	for _, b := range retained {
		if b.OplogStart == nil {
			continue
		}
		if b.OplogStart.Compare(cutoff) < 0 {
			cutoff = *b.OplogStart
		}
	}
	return cutoff, true
}

// PlanOplogPrune 은 cutoff 보다 완전히 이전인 segment 만 골라 오래된 순으로
// 반환한다.
//
// 판정에 End 를 쓴다 — End >= cutoff 면 그 segment 는 cutoff 지점을 걸칠 수
// 있으므로 남긴다 (보수적). 반대로 End < cutoff 인 segment 는 모든 보존 base
// 의 앵커보다 앞이라 어떤 window 에도 기여할 수 없다 = 순수 쓰레기.
//
// 이 성질 덕에 plan 은 **순서 무관 + 멱등**이다: 일부만 지워지고 실패해도
// 깨지는 불변식이 없다 (지워진 것도 남은 것도 모두 어차피 쓰레기). 다음
// reconcile 이 다시 list → plan 하며 자연히 재시도된다. S3 는 다중 객체 삭제의
// 원자성을 제공하지 않으므로, 원자성 대신 **부분 적용이 무해하도록 plan 을
// 구성**하는 쪽을 택했다.
func PlanOplogPrune(segments []OplogSegment, cutoff BSONTimestamp) []OplogSegment {
	victims := make([]OplogSegment, 0, len(segments))
	for _, s := range segments {
		if s.End.Compare(cutoff) < 0 {
			victims = append(victims, s)
		}
	}
	sort.Slice(victims, func(i, j int) bool {
		if c := victims[i].Start.Compare(victims[j].Start); c != 0 {
			return c < 0
		}
		return victims[i].End.Compare(victims[j].End) < 0
	})
	return victims
}

// VerifyPrunePlan 은 삭제 plan 을 적용해도 모든 보존 base 의 window 가
// 그대로인지 검증한다. 하나라도 달라지면 plan 을 폐기해야 한다.
//
// # 왜 "후퇴" 가 아니라 "불변" 인가
//
// 요구는 "삭제 후 EarliestRestore 가 후퇴하면 abort" 였지만, EarliestRestore
// 는 정의상 base 의 OplogStart 와 동치라 (api/v1alpha1 godoc) segment 삭제로
// 절대 움직이지 않는다 — 그 문자 그대로의 검사는 공허하다. 실제로 깨질 수
// 있는 건 chain 이 잘려 LatestRestore 가 앞당겨지는 쪽이다. 그래서 검사를
// **보존 base 의 window 전체가 삭제 전후로 동일** 로 일반화했다 (Latest 후퇴
// + 커버리지 상실 둘 다 포착).
//
// PlanOplogPrune 이 올바르면 이 검사는 절대 실패하지 않는다 — 비가역 삭제
// 직전의 심층 방어 (assertion) 이지 통상 경로가 아니다. 발화하면 cutoff /
// plan 계산에 버그가 있다는 뜻.
func VerifyPrunePlan(retained []BaseBackup, before, after []OplogSegment) error {
	for _, b := range retained {
		if b.OplogStart == nil {
			continue
		}
		w1 := ComputeOplogWindow(*b.OplogStart, before)
		w2 := ComputeOplogWindow(*b.OplogStart, after)
		if w1.Latest.Compare(w2.Latest) != 0 || w1.Covered != w2.Covered {
			return fmt.Errorf(
				"prune plan would shrink restorable window of backup %s: latest %s → %s (covered %t → %t)",
				b.Name, w1.Latest, w2.Latest, w1.Covered, w2.Covered)
		}
	}
	return nil
}
