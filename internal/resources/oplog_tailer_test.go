/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

// oplog_tailer_test.go — PITR oplog 증분 스트리밍 사이드카 회귀 가드.
//
// 본 test 의 핵심 관심사는 "사이드카가 *증분* 으로 *S3 에 직접* 흘리는가" 다.
// 구 구현의 3 결함 (전량 재덤프 / EmptyDir 경유 / HWM 부재) 이 되살아나면
// 즉시 fail 하도록 각 결함에 대한 negative assertion 을 함께 둔다.

package resources

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
)

// testTailerImage 는 테스트에서 주입하는 mongodump+aws 통합 이미지 레퍼런스.
// 이미지 해결(env)은 resolveOplogTailerImage 소관이고, BuildOplogTailerSidecar
// 는 이미 확보된 이미지를 인자로 받으므로 테스트도 이미지를 명시로 넘긴다
// (env 미의존 → t.Parallel 유지).
const testTailerImage = "registry.example.com/mongo-pitr:8.2"

// s3BackupSpec 는 PITR 활성 + S3 storage 인 표준 BackupSpec 을 만든다.
func s3BackupSpec() *mongodbv1alpha1.BackupSpec {
	return &mongodbv1alpha1.BackupSpec{
		Enabled:             true,
		PITREnabled:         true,
		OplogRetentionHours: 24,
		Storage: mongodbv1alpha1.BackupStorageSpec{
			Type: "s3",
			S3: &mongodbv1alpha1.S3StorageSpec{
				Bucket:         "mongo-backups",
				Endpoint:       "https://rgw.keiailab.com",
				Region:         "us-east-1",
				Prefix:         "pitr/",
				CredentialsRef: corev1.LocalObjectReference{Name: "s3-creds"},
			},
		},
	}
}

// tailerScript 는 사이드카 컨테이너에서 렌더된 shell script 를 뽑는다.
func tailerScript(t *testing.T, c corev1.Container) string {
	t.Helper()
	require.Len(t, c.Command, 3, "command 는 [/bin/bash -c <script>] 3 요소")
	return c.Command[2]
}

// codeOnly 는 셸 스크립트에서 주석 줄을 걷어낸 *실행되는 부분*만 돌려준다.
// "이 플래그가 나오면 안 된다" 류의 부정 assert 는 반드시 이걸 통과시켜야 한다 —
// 헤더 주석이 계약을 설명하며 그 플래그 이름을 언급하는 것은 정상이고, 원문
// 그대로 부정 검사하면 주석 문구에 걸려 오탐이 난다.
// (internal/assets/scripts_test.go 의 동명 helper 와 동일 규약 — 패키지가 달라
//
//	공유가 불가해 미러한다.)
func codeOnly(script string) string {
	var b strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func TestIsOplogTailerEnabled(t *testing.T) {
	t.Parallel()

	// PVC storage — 아키텍처 A 는 S3 직접 스트리밍이라 업로드 대상이 없다.
	pvcSpec := &mongodbv1alpha1.BackupSpec{
		Enabled: true, PITREnabled: true, OplogRetentionHours: 24,
		Storage: mongodbv1alpha1.BackupStorageSpec{Type: "pvc"},
	}
	// type=s3 인데 S3 블록이 nil — spec 부정합.
	s3NilSpec := &mongodbv1alpha1.BackupSpec{
		Enabled: true, PITREnabled: true, OplogRetentionHours: 24,
		Storage: mongodbv1alpha1.BackupStorageSpec{Type: "s3"},
	}
	// bucket 이 비면 키를 만들 수 없다.
	noBucket := s3BackupSpec()
	noBucket.Storage.S3.Bucket = ""

	backupDisabled := s3BackupSpec()
	backupDisabled.Enabled = false
	pitrDisabled := s3BackupSpec()
	pitrDisabled.PITREnabled = false
	zeroRetention := s3BackupSpec()
	zeroRetention.OplogRetentionHours = 0
	negRetention := s3BackupSpec()
	negRetention.OplogRetentionHours = -1

	cases := []struct {
		name string
		spec *mongodbv1alpha1.BackupSpec
		want bool
	}{
		{"nil spec", nil, false},
		{"backup disabled", backupDisabled, false},
		{"PITR disabled", pitrDisabled, false},
		{"retention 0", zeroRetention, false},
		{"retention negative", negRetention, false},
		{"pvc storage (S3 아님)", pvcSpec, false},
		{"type=s3 인데 S3 블록 nil", s3NilSpec, false},
		{"bucket 빈 값", noBucket, false},
		{"all enabled + S3", s3BackupSpec(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsOplogTailerEnabled(tc.spec); got != tc.want {
				t.Errorf("IsOplogTailerEnabled(%v) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}

func TestBuildOplogTailerSidecar_BaseSpec(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(testTailerImage, 27017, true, "rs0", s3BackupSpec())

	assert.Equal(t, "oplog-tailer", c.Name)
	require.NotEmpty(t, c.Command, "command must be set (/bin/bash -c script)")
	assert.Equal(t, "/bin/bash", c.Command[0], "bash 필요 — 10# 산술 / 배열 사용")
	assert.Equal(t, "-c", c.Command[1])

	script := tailerScript(t, c)
	for _, fragment := range []string{
		"mongodump",
		"--db=local",
		"--collection=oplog.rs",
		`--port "${PORT}"`,
		"PORT=27017", // 인자로 받은 port 가 template 로 주입됐는가
		`sleep "${BATCH_SECONDS}"`,
		"BATCH_SECONDS=30",
		`CLUSTER="rs0"`,
	} {
		assert.Contains(t, script, fragment, "oplog tailer script must contain %q", fragment)
	}

	// Volume mount 정합 — scratch + admin-credentials (when requested)
	mountNames := map[string]string{}
	for _, m := range c.VolumeMounts {
		mountNames[m.Name] = m.MountPath
	}
	assert.Equal(t, oplogStagingMount, mountNames[oplogStagingVolume])
	assert.Equal(t, "/etc/mongodb-admin", mountNames["admin-credentials"])

	// SecurityContext 가 default (non-root) 와 일치
	require.NotNil(t, c.SecurityContext)
	assert.False(t, *c.SecurityContext.AllowPrivilegeEscalation)
}

// 결함 #1 회귀 가드: "매 배치 oplog.rs 전량 재덤프".
func TestBuildOplogTailerSidecar_IncrementalQuery(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(testTailerImage, 27017, true, "rs0", s3BackupSpec())
	script := tailerScript(t, c)

	// {ts: {$gt: HWM, $lte: NOW}} extended JSON 쿼리로 dump 범위를 좁힌다.
	assert.Contains(t, script, `--query="${query}"`, "mongodump 는 --query 로 증분 범위를 받아야 한다")
	assert.Contains(t, script,
		`{"ts": {"$gt": {"$timestamp": {"t": %d, "i": %d}}, "$lte": {"$timestamp": {"t": %d, "i": %d}}}}`,
		"증분 쿼리는 $gt HWM + $lte NOW 로 양끝을 고정해야 한다")
	assert.Contains(t, script, `"${HWM_SEC}" "${HWM_INC}" "${NOW_SEC}" "${NOW_INC}"`,
		"쿼리 인자는 HWM(하한) → NOW(상한) 순")
}

// 결함 #2 회귀 가드: EmptyDir 경유 staging (pod 재시작 유실).
func TestBuildOplogTailerSidecar_StreamsDirectlyToS3(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(testTailerImage, 27017, true, "rs0", s3BackupSpec())
	script := tailerScript(t, c)

	// mongodump → gzip → aws s3 cp - 한 파이프 (capture+upload 원자).
	//
	// `--out=-` (raw BSON to stdout) 이지 `--archive` 가 아니다 — restore-fetch
	// 가 세그먼트를 gunzip 해 단일 oplog.bson 으로 *연접*하고 mongorestore
	// --dir 로 replay 하는데, archive 포맷은 prelude 가 있어 연접이 불가능하다.
	// (mongodump --help 실측: `-o, --out=<directory-path>, or '-' for stdout`
	//  vs `--archive=<file-path>  dump as an archive`.)
	assert.Contains(t, script, "--quiet --out=-", "세그먼트는 raw BSON 이어야 연접 가능하다")
	assert.NotContains(t, codeOnly(script), "--archive",
		"archive 포맷은 연접 불가 — restore-fetch 의 gunzip+concat 계약과 충돌")
	assert.Contains(t, script, "| gzip -c", "gzip 스트리밍")
	assert.Contains(t, script, `| aws_s3 cp - "${key}"`, "S3 로 직접 스트리밍")
	assert.Contains(t, script, "set -euo pipefail",
		"pipefail 없으면 mongodump 실패 + aws 성공 = 잘린 세그먼트를 성공으로 오인")

	// 구 구현의 staging 파일 산출이 되살아나면 fail.
	assert.NotContains(t, script, `--archive="${OUT}"`, "staging 파일 경유 금지 — S3 직접 스트리밍")
	assert.NotContains(t, script, "oplog-${TS}.bson", "timestamped staging 파일 산출 금지")

	// scratch 마운트는 batch 저장용이 아니라 HOME/TMPDIR 용도로만 쓰인다.
	assert.Contains(t, script, `export HOME="${WORK_DIR}"`)
	assert.Contains(t, script, `export TMPDIR="${WORK_DIR}"`)
}

// 결함 #3 회귀 가드: HWM/resume token 부재.
func TestBuildOplogTailerSidecar_HWMFromS3KeyOnly(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(testTailerImage, 27017, true, "rs0", s3BackupSpec())
	script := tailerScript(t, c)

	// 부팅 시 S3 최신 세그먼트 키에서 HWM 복원 — 별도 상태 저장소 0.
	assert.Contains(t, script, `aws_s3 ls "s3://${S3_BUCKET}/${OPLOG_PREFIX}/"`)
	assert.Contains(t, script, "| sort | tail -1", "사전식 정렬 == 시간순 → 최신 세그먼트")
	assert.Contains(t, script, `^[0-9]{10}-[0-9]{10}_[0-9]{10}-[0-9]{10}\.bson\.gz$`,
		"세그먼트 키 필터가 키 스킴과 정확히 일치해야 한다")
	assert.Contains(t, script, "HWM_SEC=$(( 10#${end%%-*} ))", "endTs 에서 HWM 파생")
	assert.Contains(t, script, "HWM_INC=$(( 10#${end#*-} ))")

	// 업로드 성공 후에만 HWM 전진 (실패 시 유지 → 재시도 → gap 방지).
	uploadIdx := strings.Index(script, `| aws_s3 cp - "${key}"; then`)
	advanceIdx := strings.Index(script, `HWM_SEC="${NOW_SEC}"; HWM_INC="${NOW_INC}"`)
	require.Positive(t, uploadIdx, "업로드 파이프가 if 조건이어야 한다")
	require.Positive(t, advanceIdx, "HWM 전진 라인이 있어야 한다")
	assert.Greater(t, advanceIdx, uploadIdx, "HWM 전진은 업로드 성공 분기 안에서만")

	// 실패 경로: 상류가 죽어도 aws 는 받은 조각으로 업로드를 완료할 수 있다.
	// 그 잘린 객체를 지우지 않으면 startTs 가 같고 endTs 만 짧은 조각이 쌓여
	// restore 가 그걸 집는다 (실측 확인 후 추가).
	assert.Contains(t, script, `aws_s3 rm "${key}"`, "실패 시 잘린 객체를 best-effort 로 제거해야 한다")
}

// S3 키 스킴 — restore / uploader 트랙과의 계약. 깨지면 3 트랙이 동시에 깨진다.
func TestBuildOplogTailerSidecar_S3KeyScheme(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(testTailerImage, 27017, true, "rs0", s3BackupSpec())
	script := tailerScript(t, c)

	// <ts> = %010d-%010d (sec-inc). 고정폭 zero-pad → 사전식 정렬 = 시간순.
	assert.Contains(t, script, `ts_key() { printf '%010d-%010d' "$1" "$2"; }`,
		"ts 포맷은 sec/inc 각 10 자리 zero-pad")
	// 전체 키 = <prefix>/<cluster>/oplog/<startTs>_<endTs>.bson.gz
	assert.Contains(t, script, `key="s3://${S3_BUCKET}/${OPLOG_PREFIX}/${start_key}_${end_key}.bson.gz"`)
	assert.Contains(t, script, `OPLOG_PREFIX="${PFX}/${CLUSTER}/oplog"`)
	assert.Contains(t, script, `OPLOG_PREFIX="${CLUSTER}/oplog"`, "S3_PREFIX 빈 값이면 prefix 없이")
	assert.Contains(t, script, `PFX="${PFX%/}"`, "trailing slash 정규화 — 이중 슬래시 방지")
}

// gap 감지 — oplog capped rollover 로 HWM 구간이 사라진 경우.
func TestBuildOplogTailerSidecar_GapDetection(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(testTailerImage, 27017, true, "rs0", s3BackupSpec())
	script := tailerScript(t, c)

	assert.Contains(t, script, "GAP DETECTED", "gap 은 반드시 로그로 드러나야 한다")
	assert.Contains(t, script, "check_gap", "매 batch gap 검사 — 부팅 직후뿐 아니라 tail 지연도 gap")
	assert.Contains(t, script, "oplog_edge_ts 1", "oplog 최초 entry 와 HWM 비교")
	// HWM rewind = 키 체인에 구멍을 노출시키는 장치 (침묵 gap 방지).
	assert.Contains(t, script, `ts_prev "${old_sec}" "${old_inc}"`)
	assert.Contains(t, script, "ts_prev() {")
}

// 단일 writer — 세그먼트는 PRIMARY 만 쓴다 (N 개 사이드카 중복 업로드 차단).
func TestBuildOplogTailerSidecar_PrimaryOnlyWriter(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(testTailerImage, 27017, true, "rs0", s3BackupSpec())
	script := tailerScript(t, c)

	assert.Contains(t, script, "db.hello().isWritablePrimary === true")
	assert.Contains(t, script, "if is_primary; then", "primary 가 아니면 batch 를 건너뛴다")
	assert.Contains(t, script, "not PRIMARY — skip batch")
	// 구 구현의 잘못된 readPreference (localhost 직결 사이드카엔 무의미).
	assert.NotContains(t, script, "--readPreference=secondary")
}

// S3 env 주입 — BuildBackupJob 의 블록과 동일 계약.
func TestBuildOplogTailerSidecar_S3EnvInjection(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(testTailerImage, 27017, true, "rs0", s3BackupSpec())

	env := map[string]corev1.EnvVar{}
	for _, e := range c.Env {
		env[e.Name] = e
	}
	assert.Equal(t, "mongo-backups", env["S3_BUCKET"].Value)
	assert.Equal(t, "https://rgw.keiailab.com", env["S3_ENDPOINT"].Value)
	assert.Equal(t, "us-east-1", env["S3_REGION"].Value)
	assert.Equal(t, "pitr/", env["S3_PREFIX"].Value)

	ak := env["AWS_ACCESS_KEY_ID"]
	require.NotNil(t, ak.ValueFrom, "AWS_ACCESS_KEY_ID 는 Secret 참조")
	require.NotNil(t, ak.ValueFrom.SecretKeyRef)
	assert.Equal(t, "s3-creds", ak.ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "access-key", ak.ValueFrom.SecretKeyRef.Key)

	sk := env["AWS_SECRET_ACCESS_KEY"]
	require.NotNil(t, sk.ValueFrom, "AWS_SECRET_ACCESS_KEY 는 Secret 참조")
	require.NotNil(t, sk.ValueFrom.SecretKeyRef)
	assert.Equal(t, "s3-creds", sk.ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "secret-key", sk.ValueFrom.SecretKeyRef.Key)

	// 평문 시크릿이 env value 로 새면 안 된다.
	assert.Empty(t, ak.Value)
	assert.Empty(t, sk.Value)
}

func TestBuildOplogS3EnvVars_NonS3ReturnsNil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, buildOplogS3EnvVars(nil))
	assert.Nil(t, buildOplogS3EnvVars(&mongodbv1alpha1.BackupSpec{
		Storage: mongodbv1alpha1.BackupStorageSpec{Type: "pvc"},
	}), "PVC storage 에는 S3 env 를 주입하지 않는다")
	assert.Nil(t, buildOplogS3EnvVars(&mongodbv1alpha1.BackupSpec{
		Storage: mongodbv1alpha1.BackupStorageSpec{Type: "s3"},
	}), "S3 블록이 nil 이면 nil (역참조 panic 차단)")
}

func TestBuildOplogTailerSidecar_NoAdminSecret(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(testTailerImage, 27018, false, "rs0", s3BackupSpec())
	for _, m := range c.VolumeMounts {
		assert.NotEqual(t, "admin-credentials", m.Name, "admin secret mount must be omitted")
	}
	// 인증 미사용 클러스터에 -u/-p 를 넘기면 오히려 실패 → 조건부 구성.
	script := tailerScript(t, c)
	assert.Contains(t, script, "AUTH_ARGS=()")
	assert.Contains(t, script, `if [ -n "${ADMIN_PASS}" ]; then`)
}

func TestBuildOplogTailerSidecar_PortOverride(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(testTailerImage, 27019, false, "cfg", s3BackupSpec())
	assert.Contains(t, tailerScript(t, c), "PORT=27019", "config server port must be propagated")
}

func TestBuildOplogTailerSidecar_PortZeroFallback(t *testing.T) {
	t.Parallel()
	// 0 또는 음수 port 가 들어오면 기본값(mongoDBPort=27017) 사용.
	c := BuildOplogTailerSidecar(testTailerImage, 0, false, "rs0", s3BackupSpec())
	assert.Contains(t, tailerScript(t, c), "PORT=27017", "zero port must fall back to mongoDBPort")
}

// 이미지 해결 = env 유일. 구 구현의 mongo 폴백은 제거됐다 (aws 부재 crash 방지).
// 미설정은 조용히 끄지 않고 fail-open skip 사유(reason)를 노출해야 한다.
// (env 를 만지므로 t.Parallel 을 쓰지 않는다 — t.Setenv 와 상호 배타.)
func TestResolveOplogTailerImage(t *testing.T) {
	// 미설정(빈 값 포함) — 폴백 없이 이미지 없음 + 사유.
	t.Setenv(oplogTailerImageEnv, "")
	img, reason := resolveOplogTailerImage()
	assert.Empty(t, img, "미설정이면 이미지 없음 — mongo 폴백 금지")
	require.NotEmpty(t, reason, "미설정은 조용히 끄지 않고 사유를 노출해야 한다")
	assert.Contains(t, reason, oplogTailerImageEnv, "사유는 어떤 env 를 세팅해야 하는지 알려준다")

	// 설정 — 그 이미지를 그대로, skip 사유 없이 반환.
	t.Setenv(oplogTailerImageEnv, "registry.example.com/mongo-pitr:8.2")
	img, reason = resolveOplogTailerImage()
	assert.Equal(t, "registry.example.com/mongo-pitr:8.2", img)
	assert.Empty(t, reason, "이미지가 있으면 skip 사유 없음")
}

// 호출자가 확보한 이미지가 컨테이너 Image 로 그대로 박히는가 (env 해석은 resolver 소관).
func TestBuildOplogTailerSidecar_ImagePassthrough(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar("registry.example.com/mongo-pitr:8.2", 27017, false, "rs0", s3BackupSpec())
	assert.Equal(t, "registry.example.com/mongo-pitr:8.2", c.Image,
		"사이드카 Image 는 호출자가 준 통합 이미지여야 한다")
}

// aws CLI 부재 시 조용히 도는 대신 즉시 죽어야 한다 (침묵 gap 방지).
func TestBuildOplogTailerSidecar_FailsLoudlyWithoutAwsCLI(t *testing.T) {
	t.Parallel()
	c := BuildOplogTailerSidecar(testTailerImage, 27017, true, "rs0", s3BackupSpec())
	script := tailerScript(t, c)

	assert.Contains(t, script, "ensure_aws")
	assert.Contains(t, script, "FATAL: aws CLI 부재")
	assert.Contains(t, script, "exit 1", "업로드 불가 상태로 계속 돌면 안 된다")
	assert.Contains(t, script, `: "${S3_BUCKET:?`, "S3_BUCKET 부재도 즉시 실패")
}

func TestBuildOplogStagingVolume_EmptyDirWithLimit(t *testing.T) {
	t.Parallel()
	v := BuildOplogStagingVolume()
	assert.Equal(t, oplogStagingVolume, v.Name)
	require.NotNil(t, v.EmptyDir, "must be EmptyDir (not PVC)")
	require.NotNil(t, v.EmptyDir.SizeLimit)
	// batch 를 쌓지 않으므로 scratch 1Gi 로 충분 (구 4Gi = staging 한도).
	q := v.EmptyDir.SizeLimit
	assert.Equal(t, int64(1024*1024*1024), q.Value())
}
