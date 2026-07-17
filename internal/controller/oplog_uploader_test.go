/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// oplog_uploader_test.go — PITR uploader (window 계산 + retention GC) 회귀 가드.
//
// 대상은 backup_gc.go 의 순수 도메인 로직 — 네트워크 0 / API 서버 0 / 시계
// 의존 0 (now 는 전부 주입) 이라 결정론적이다.

package controller

import (
	"testing"
	"time"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

func TestOplogUploaderReconciler_IsApplicable(t *testing.T) {
	t.Parallel()
	r := &OplogUploaderReconciler{}
	cases := []struct {
		name string
		spec *mongodbv1alpha1.BackupSpec
		want bool
	}{
		{"nil", nil, false},
		{"backup disabled", &mongodbv1alpha1.BackupSpec{Enabled: false, PITREnabled: true, OplogRetentionHours: 24}, false},
		{"PITR disabled", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: false, OplogRetentionHours: 24}, false},
		{"retention zero", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: true, OplogRetentionHours: 0}, false},
		{"retention positive", &mongodbv1alpha1.BackupSpec{Enabled: true, PITREnabled: true, OplogRetentionHours: 24}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := r.IsApplicable(tc.spec); got != tc.want {
				t.Errorf("IsApplicable(%v) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}

func TestBSONTimestamp_Compare(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b BSONTimestamp
		want int
	}{
		{"equal", ts(100, 5), ts(100, 5), 0},
		{"sec less", ts(99, 9), ts(100, 0), -1},
		{"sec greater", ts(101, 0), ts(100, 9), 1},
		{"ordinal less", ts(100, 4), ts(100, 5), -1},
		{"ordinal greater", ts(100, 6), ts(100, 5), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.a.Compare(tc.b); got != tc.want {
				t.Errorf("%s.Compare(%s) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// 키 왕복 — FormatOplogSegmentKey 가 만든 키를 ParseOplogSegmentKey 가 그대로
// 되읽어야 한다 (tailer 와 공유하는 계약의 self-consistency).
func TestOplogSegmentKey_RoundTrip(t *testing.T) {
	t.Parallel()
	start, end := ts(1752710400, 1), ts(1752713400, 9)
	key := FormatOplogSegmentKey("backups/", "mycluster", start, end)

	wantKey := "backups/mycluster/oplog/1752710400-0000000001_1752713400-0000000009.bson.gz"
	if key != wantKey {
		t.Fatalf("FormatOplogSegmentKey = %q, want %q", key, wantKey)
	}
	seg, ok := ParseOplogSegmentKey(key)
	if !ok {
		t.Fatalf("ParseOplogSegmentKey(%q) failed", key)
	}
	if seg.Start.Compare(start) != 0 || seg.End.Compare(end) != 0 {
		t.Errorf("round trip = [%s, %s], want [%s, %s]", seg.Start, seg.End, start, end)
	}
	if seg.Key != key {
		t.Errorf("seg.Key = %q, want %q", seg.Key, key)
	}
}

// prefix 정규화는 키를 실제로 쓰는 shell (oplog-stream.sh.tpl 의
// `PFX="${S3_PREFIX%/}"` → "/" join) 과 반드시 동일해야 한다. 어긋나면 tailer 가
// 쓴 세그먼트를 uploader 가 못 찾아 window 가 조용히 비고 GC 도 멈춘다.
func TestOplogSegmentPrefix_MatchesShellNormalization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, s3Prefix, want string
	}{
		{"슬래시로 끝나는 prefix", "pitr/", "pitr/rs0/oplog/"},
		{"슬래시 없는 prefix", "pitr", "pitr/rs0/oplog/"},
		{"빈 prefix", "", "rs0/oplog/"},
		{"중첩 prefix", "a/b/", "a/b/rs0/oplog/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := OplogSegmentPrefix(tc.s3Prefix, "rs0"); got != tc.want {
				t.Errorf("OplogSegmentPrefix(%q, \"rs0\") = %q, want %q", tc.s3Prefix, got, tc.want)
			}
		})
	}
}

// zero-pad 는 사전식 정렬 = 시간순을 위한 것이므로 고정폭이어야 한다.
func TestFormatOplogSegmentKey_LexicographicOrder(t *testing.T) {
	t.Parallel()
	early := FormatOplogSegmentKey("p/", "c", ts(999, 0), ts(1000, 0))
	late := FormatOplogSegmentKey("p/", "c", ts(1000, 0), ts(1001, 0))
	if early >= late {
		t.Errorf("zero-pad broken: %q should sort before %q", early, late)
	}
}

func TestParseOplogSegmentKey_Invalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  string
	}{
		{"wrong extension", "p/c/oplog/1000-0_2000-0.bson"},
		{"base archive", "p/c-20260717-010000.archive.gz"},
		{"meta json", "p/c/oplog/base.meta.json"},
		{"missing range separator", "p/c/oplog/1000-0.bson.gz"},
		{"missing ts separator", "p/c/oplog/1000_2000.bson.gz"},
		{"non numeric sec", "p/c/oplog/abc-0_2000-0.bson.gz"},
		{"non numeric ordinal", "p/c/oplog/1000-x_2000-0.bson.gz"},
		{"empty ordinal", "p/c/oplog/1000-_2000-0.bson.gz"},
		{"uint32 overflow", "p/c/oplog/4294967296-0_4294967297-0.bson.gz"},
		{"start after end", "p/c/oplog/2000-0_1000-0.bson.gz"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, ok := ParseOplogSegmentKey(tc.key); ok {
				t.Errorf("ParseOplogSegmentKey(%q) = ok, want rejected", tc.key)
			}
		})
	}
}

func TestComputeOplogWindow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		base        BSONTimestamp
		segments    []OplogSegment
		wantLatest  BSONTimestamp
		wantCovered bool
		wantGap     bool
	}{
		{
			name:        "no segments — base 시점 복원만",
			base:        ts(100, 0),
			segments:    nil,
			wantLatest:  ts(100, 0),
			wantCovered: false,
		},
		{
			name:        "contiguous chain",
			base:        ts(100, 0),
			segments:    segs(seg(100, 0, 130, 0), seg(130, 0, 160, 0), seg(160, 0, 190, 0)),
			wantLatest:  ts(190, 0),
			wantCovered: true,
		},
		{
			name: "unsorted input — 정렬 후 동일 결과",
			base: ts(100, 0),
			// 입력 순서를 뒤섞어도 chain 이 온전해야 한다.
			segments:    segs(seg(160, 0, 190, 0), seg(100, 0, 130, 0), seg(130, 0, 160, 0)),
			wantLatest:  ts(190, 0),
			wantCovered: true,
		},
		{
			name:        "overlapping segments",
			base:        ts(100, 0),
			segments:    segs(seg(100, 0, 150, 0), seg(120, 0, 130, 0), seg(140, 0, 200, 0)),
			wantLatest:  ts(200, 0),
			wantCovered: true,
		},
		{
			name:        "gap mid-chain — gap 앞에서 잘린다",
			base:        ts(100, 0),
			segments:    segs(seg(100, 0, 130, 0), seg(160, 0, 190, 0)),
			wantLatest:  ts(130, 0),
			wantCovered: true,
			wantGap:     true,
		},
		{
			name:        "gap right after base — 커버 못 함",
			base:        ts(100, 0),
			segments:    segs(seg(110, 0, 190, 0)),
			wantLatest:  ts(100, 0),
			wantCovered: false,
			wantGap:     true,
		},
		{
			name: "segments entirely before base — 무시",
			base: ts(100, 0),
			// prune 이 base 직후 segment 를 지운 상황 → base 시점 복원만 가능.
			segments:    segs(seg(10, 0, 20, 0), seg(20, 0, 30, 0)),
			wantLatest:  ts(100, 0),
			wantCovered: false,
		},
		{
			name:        "segment spans base — 앵커로 인정",
			base:        ts(100, 0),
			segments:    segs(seg(90, 0, 150, 0)),
			wantLatest:  ts(150, 0),
			wantCovered: true,
		},
		{
			name:        "ordinal 경계 — 같은 초 안에서 이어짐",
			base:        ts(100, 0),
			segments:    segs(seg(100, 0, 100, 5), seg(100, 5, 100, 9)),
			wantLatest:  ts(100, 9),
			wantCovered: true,
		},
		{
			name:        "ordinal 경계 — 같은 초 안에서 끊김",
			base:        ts(100, 0),
			segments:    segs(seg(100, 0, 100, 5), seg(100, 7, 100, 9)),
			wantLatest:  ts(100, 5),
			wantCovered: true,
			wantGap:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ComputeOplogWindow(tc.base, tc.segments)
			if got.Base.Compare(tc.base) != 0 {
				t.Errorf("Base = %s, want %s", got.Base, tc.base)
			}
			if got.Latest.Compare(tc.wantLatest) != 0 {
				t.Errorf("Latest = %s, want %s", got.Latest, tc.wantLatest)
			}
			if got.Covered != tc.wantCovered {
				t.Errorf("Covered = %t, want %t", got.Covered, tc.wantCovered)
			}
			if gotGap := got.GapFrom != nil; gotGap != tc.wantGap {
				t.Errorf("gap detected = %t, want %t", gotGap, tc.wantGap)
			}
			if got.GapFrom != nil && got.GapTo == nil {
				t.Error("GapFrom set but GapTo nil")
			}
		})
	}
}

// PITR acceptance 의 window 측면 모델 — base 스냅샷 이후 doc-A(t1) / doc-B(t2)
// 가 segment 로 아카이브됐다면 t1+1s 는 window 안이어야 한다 (복원 가능).
func TestComputeOplogWindow_AcceptanceShape(t *testing.T) {
	t.Parallel()
	base := ts(1000, 0)
	t1, t2 := ts(1001, 0), ts(1004, 0)
	w := ComputeOplogWindow(base, segs(seg(1000, 0, 1010, 0)))

	if !w.Covered {
		t.Fatal("chain should cover base")
	}
	target := ts(t1.Sec+1, 0)
	if target.Compare(w.Base) < 0 || target.Compare(w.Latest) > 0 {
		t.Errorf("PIT %s outside window [%s, %s]", target, w.Base, w.Latest)
	}
	if t2.Compare(w.Latest) > 0 {
		t.Errorf("t2 %s should also be inside window ending %s", t2, w.Latest)
	}
	if got, want := w.Duration(), 10*time.Second; got != want {
		t.Errorf("Duration = %s, want %s", got, want)
	}
}

func TestPlanBackupRetention(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	// newest → oldest: b0(1d) b1(3d) b2(10d) b3(30d)
	all := []BaseBackup{
		{Name: "b2", CreatedAt: now.Add(-10 * 24 * time.Hour)},
		{Name: "b0", CreatedAt: now.Add(-1 * 24 * time.Hour)},
		{Name: "b3", CreatedAt: now.Add(-30 * 24 * time.Hour)},
		{Name: "b1", CreatedAt: now.Add(-3 * 24 * time.Hour)},
	}

	cases := []struct {
		name        string
		retention   *mongodbv1alpha1.RetentionSpec
		wantExpired []string
	}{
		{"nil retention — 정책 부재", nil, nil},
		{"both unset — 전부 보존 (공허참 가드)", &mongodbv1alpha1.RetentionSpec{Days: 0}, nil},
		{"count zero — 미설정 취급", &mongodbv1alpha1.RetentionSpec{Days: 0, Count: intp(0)}, nil},
		{"days only", &mongodbv1alpha1.RetentionSpec{Days: 7}, []string{"b2", "b3"}},
		{"count only", &mongodbv1alpha1.RetentionSpec{Count: intp(2)}, []string{"b2", "b3"}},
		{
			// b2(10d) 는 나이는 걸리지만 최신 3개 안이라 보존 — 둘 다 만족해야 삭제.
			name:        "days ∧ count — count 가 구제",
			retention:   &mongodbv1alpha1.RetentionSpec{Days: 7, Count: intp(3)},
			wantExpired: []string{"b3"},
		},
		{
			// b1(3d) 은 순위는 밀리지만 나이가 안 됐으므로 보존.
			name:        "days ∧ count — days 가 구제",
			retention:   &mongodbv1alpha1.RetentionSpec{Days: 7, Count: intp(1)},
			wantExpired: []string{"b2", "b3"},
		},
		{"days ∧ count — 전부 보존", &mongodbv1alpha1.RetentionSpec{Days: 90, Count: intp(10)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan := PlanBackupRetention(all, tc.retention, now)
			if got := names(plan.Expired); !equalStrings(got, tc.wantExpired) {
				t.Errorf("Expired = %v, want %v", got, tc.wantExpired)
			}
			if total := len(plan.Retained) + len(plan.Expired); total != len(all) {
				t.Errorf("retained+expired = %d, want %d (백업 유실)", total, len(all))
			}
		})
	}
}

// CompletionTime 이 있으면 나이 기준이 그쪽 — 오래 걸린 백업이 조기 만료되지
// 않도록.
func TestPlanBackupRetention_SortsNewestFirst(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	all := []BaseBackup{
		{Name: "old", CreatedAt: now.Add(-5 * time.Hour)},
		{Name: "new", CreatedAt: now.Add(-1 * time.Hour)},
		{Name: "mid", CreatedAt: now.Add(-3 * time.Hour)},
	}
	plan := PlanBackupRetention(all, &mongodbv1alpha1.RetentionSpec{Count: intp(1)}, now)
	if got := names(plan.Retained); !equalStrings(got, []string{"new"}) {
		t.Errorf("Retained = %v, want [new] (최신 1개)", got)
	}
	if got := names(plan.Expired); !equalStrings(got, []string{"mid", "old"}) {
		t.Errorf("Expired = %v, want [mid old]", got)
	}
}

func TestOplogGCCutoff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	nowTS := bsonTSFromTime(now)

	t.Run("retention 미설정 — GC 안 함", func(t *testing.T) {
		t.Parallel()
		if _, ok := OplogGCCutoff(nil, 0, now); ok {
			t.Error("OplogGCCutoff(hours=0) = ok, want skip")
		}
	})

	t.Run("보존 base 없음 — 시간 cutoff 만", func(t *testing.T) {
		t.Parallel()
		got, ok := OplogGCCutoff(nil, 24, now)
		if !ok {
			t.Fatal("want ok")
		}
		if want := ts(nowTS.Sec-24*3600, 0); got.Compare(want) != 0 {
			t.Errorf("cutoff = %s, want %s", got, want)
		}
	})

	t.Run("base floor 가 더 이르면 base 채택 (base 커버 불변식)", func(t *testing.T) {
		t.Parallel()
		// 48 시간 전 base 가 아직 보존 중 → 24h retention 으로 잘라내면
		// 그 base 의 replay 하한을 먹는다.
		older := ts(nowTS.Sec-48*3600, 0)
		got, ok := OplogGCCutoff([]BaseBackup{
			{Name: "b0", OplogStart: &older},
			{Name: "b1", OplogStart: tsp(nowTS.Sec-2*3600, 0)},
		}, 24, now)
		if !ok {
			t.Fatal("want ok")
		}
		if got.Compare(older) != 0 {
			t.Errorf("cutoff = %s, want %s (최고령 보존 base 앵커)", got, older)
		}
	})

	t.Run("시간 cutoff 가 더 이르면 시간 채택", func(t *testing.T) {
		t.Parallel()
		// base 가 전부 최근 → 시간 기반이 더 넓게 보존.
		got, ok := OplogGCCutoff([]BaseBackup{
			{Name: "b0", OplogStart: tsp(nowTS.Sec-1*3600, 0)},
		}, 24, now)
		if !ok {
			t.Fatal("want ok")
		}
		if want := ts(nowTS.Sec-24*3600, 0); got.Compare(want) != 0 {
			t.Errorf("cutoff = %s, want %s", got, want)
		}
	})

	t.Run("OplogStart 없는 base 는 floor 에 기여 안 함", func(t *testing.T) {
		t.Parallel()
		got, ok := OplogGCCutoff([]BaseBackup{{Name: "no-oplog"}}, 24, now)
		if !ok {
			t.Fatal("want ok")
		}
		if want := ts(nowTS.Sec-24*3600, 0); got.Compare(want) != 0 {
			t.Errorf("cutoff = %s, want %s", got, want)
		}
	})
}

func TestPlanOplogPrune(t *testing.T) {
	t.Parallel()
	all := segs(
		seg(100, 0, 130, 0), // End < 200 → 삭제
		seg(130, 0, 199, 9), // End < 200 → 삭제
		seg(199, 0, 200, 0), // End == 200 → 경계 걸침 → 보존
		seg(200, 0, 230, 0), // End > 200 → 보존
	)
	victims := PlanOplogPrune(all, ts(200, 0))
	if got, want := len(victims), 2; got != want {
		t.Fatalf("victims = %d, want %d", got, want)
	}
	// 오래된 순 정렬 — 부분 실패 시에도 자연스러운 순서.
	if victims[0].Start.Compare(ts(100, 0)) != 0 || victims[1].Start.Compare(ts(130, 0)) != 0 {
		t.Errorf("victims not sorted oldest-first: %s, %s", victims[0].Start, victims[1].Start)
	}
	for _, v := range victims {
		if v.End.Compare(ts(200, 0)) >= 0 {
			t.Errorf("victim %s ends at/after cutoff — 경계 걸침 segment 를 지우면 안 된다", v.Key)
		}
	}
}

func TestPlanOplogPrune_NeverBreaksRetainedWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	nowTS := bsonTSFromTime(now)

	base := ts(nowTS.Sec-3*3600, 0) // 3 시간 전 base — 아직 보존
	retained := []BaseBackup{{Name: "b0", OplogStart: &base}}
	all := segs(
		seg(nowTS.Sec-10*3600, 0, nowTS.Sec-9*3600, 0), // base 훨씬 이전 = 쓰레기
		seg(nowTS.Sec-3*3600, 0, nowTS.Sec-2*3600, 0),  // chain
		seg(nowTS.Sec-2*3600, 0, nowTS.Sec-1*3600, 0),  // chain
	)

	// 1h retention 만 순진하게 적용하면 base 의 chain 을 먹는다.
	cutoff, ok := OplogGCCutoff(retained, 1, now)
	if !ok {
		t.Fatal("want ok")
	}
	victims := PlanOplogPrune(all, cutoff)
	if got, want := len(victims), 1; got != want {
		t.Fatalf("victims = %d, want %d (base 커버 밖 1건만)", got, want)
	}

	before := ComputeOplogWindow(base, all)
	after := ComputeOplogWindow(base, survivorsOf(all, victims))
	if before.Latest.Compare(after.Latest) != 0 {
		t.Errorf("window shrank: %s → %s", before.Latest, after.Latest)
	}
	if err := VerifyPrunePlan(retained, all, survivorsOf(all, victims)); err != nil {
		t.Errorf("VerifyPrunePlan rejected a correct plan: %v", err)
	}
}

func TestVerifyPrunePlan(t *testing.T) {
	t.Parallel()
	base := ts(100, 0)
	retained := []BaseBackup{{Name: "b0", OplogStart: &base}}
	all := segs(seg(100, 0, 130, 0), seg(130, 0, 160, 0))

	t.Run("무해한 plan 통과", func(t *testing.T) {
		t.Parallel()
		// base 이전 잔재만 지우는 plan.
		withJunk := append(segs(seg(10, 0, 20, 0)), all...)
		if err := VerifyPrunePlan(retained, withJunk, all); err != nil {
			t.Errorf("want pass, got %v", err)
		}
	})

	t.Run("chain 중간 삭제 → abort", func(t *testing.T) {
		t.Parallel()
		// 두 번째 segment 를 지우면 latest 가 160 → 130 으로 후퇴.
		err := VerifyPrunePlan(retained, all, segs(seg(100, 0, 130, 0)))
		if err == nil {
			t.Fatal("want abort error, got nil")
		}
	})

	t.Run("앵커 삭제 → 커버 상실 → abort", func(t *testing.T) {
		t.Parallel()
		err := VerifyPrunePlan(retained, all, segs(seg(130, 0, 160, 0)))
		if err == nil {
			t.Fatal("want abort error, got nil")
		}
	})

	t.Run("OplogStart 없는 base 는 검사 대상 아님", func(t *testing.T) {
		t.Parallel()
		if err := VerifyPrunePlan([]BaseBackup{{Name: "no-oplog"}}, all, nil); err != nil {
			t.Errorf("want pass, got %v", err)
		}
	})
}

func TestFormatRestorableWindow(t *testing.T) {
	t.Parallel()
	w := OplogWindow{
		Base:   bsonTSFromTime(time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)),
		Latest: bsonTSFromTime(time.Date(2026, 7, 17, 9, 30, 0, 0, time.UTC)),
	}
	want := "2026-07-17T01:00:00Z ~ 2026-07-17T09:30:00Z"
	if got := formatRestorableWindow(w); got != want {
		t.Errorf("formatRestorableWindow = %q, want %q", got, want)
	}
}

func TestBsonTSFromTime_Clamps(t *testing.T) {
	t.Parallel()
	if got := bsonTSFromTime(time.Time{}); got.Sec != 0 {
		t.Errorf("zero time → %s, want 0:0 (clamp)", got)
	}
	if got := bsonTSFromTime(time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)); got.Sec != 4294967295 {
		t.Errorf("year 2200 → %s, want MaxUint32 (clamp)", got)
	}
}

// --- helpers ---

func ts(sec, ordinal uint32) BSONTimestamp { return BSONTimestamp{Sec: sec, Ordinal: ordinal} }

func tsp(sec, ordinal uint32) *BSONTimestamp {
	v := ts(sec, ordinal)
	return &v
}

func intp(i int) *int { return &i }

// seg 는 [startSec:startOrd, endSec:endOrd] segment 를 정식 키와 함께 만든다.
func seg(startSec, startOrd, endSec, endOrd uint32) OplogSegment {
	start, end := ts(startSec, startOrd), ts(endSec, endOrd)
	return OplogSegment{
		Key:   FormatOplogSegmentKey("p/", "c", start, end),
		Start: start,
		End:   end,
	}
}

func segs(s ...OplogSegment) []OplogSegment { return s }

func survivorsOf(all, victims []OplogSegment) []OplogSegment {
	doomed := make(map[string]bool, len(victims))
	for _, v := range victims {
		doomed[v.Key] = true
	}
	out := make([]OplogSegment, 0, len(all))
	for _, s := range all {
		if !doomed[s.Key] {
			out = append(out, s)
		}
	}
	return out
}

func names(bs []BaseBackup) []string {
	if len(bs) == 0 {
		return nil
	}
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Name)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
