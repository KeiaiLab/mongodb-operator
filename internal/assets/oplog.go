/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// oplog.go — PITR oplog 스트리밍 tailer 스크립트 렌더러.
//
// scripts.go 의 render()/scriptFS(`//go:embed scripts/*.tpl`) 를 그대로 재사용한다.
// 별도 파일로 둔 이유는 backup/restore 스크립트 렌더러와 소유 트랙이 다르기 때문.
package assets

// oplogStreamData는 oplog-stream.sh.tpl 렌더 컨텍스트.
type oplogStreamData struct {
	Port         int
	ClusterName  string
	WorkDir      string
	BatchSeconds int
}

// RenderOplogStream는 PITR oplog 증분 스트리밍 tailer 스크립트를 반환.
//
// port는 RS=27017 / cfg=27019 / shard=27018. clusterName은 S3 키의 `<cluster>`
// 세그먼트(`<prefix>/<cluster>/oplog/<startTs>_<endTs>.bson.gz`). workDir은
// 컨테이너의 HOME/TMPDIR(aws CLI 캐시 등 — oplog batch 자체는 S3 로 직접
// 스트리밍되므로 여기 남지 않는다). batchSeconds는 tail 회전 간격.
func RenderOplogStream(port int, clusterName, workDir string, batchSeconds int) (string, error) {
	return render("scripts/oplog-stream.sh.tpl", oplogStreamData{
		Port:         port,
		ClusterName:  clusterName,
		WorkDir:      workDir,
		BatchSeconds: batchSeconds,
	})
}
