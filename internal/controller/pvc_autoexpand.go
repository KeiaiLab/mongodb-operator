/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// pvc_autoexpand.go — Level-V Auto Pilot PVC 자동 확장 배선.
//
// auto_healing.go 의 순수 함수 PlanPVCExpansion 을 reconcile loop 에 연결한다.
// 사용률 측정은 mongod dbStats 의 fsUsedSize/fsTotalSize(데이터 파일시스템 실사용량,
// MongoDB 4.4+)를 사용한다 — 별도 RBAC(nodes/proxy)나 pod exec 인프라 없이 기존
// mongo 연결만 재사용한다. opt-in(spec.autoHealing.enabled) 이므로 비활성 시 비용 0.
package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	commonspvc "github.com/keiailab/keiailab-commons/pkg/pvc"
	"go.mongodb.org/mongo-driver/v2/bson"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	"github.com/keiailab/mongodb-operator/internal/mongodb"
)

const (
	mongodPort = 27017
	// adminUserDB — MongoDB admin database 이자 root user 명(internal/mongodb
	// 의 adminUserDB 미러). dbStats 연결의 user/authSource/command DB 가 모두
	// 동일 문자열이므로 단일 상수로 통일한다.
	adminUserDB = "admin"
)

// pvcUsageReader 는 데이터 볼륨 파일시스템 사용률(%)을 보고한다.
// 실 구현(dbStatsUsageReader)은 mongod dbStats 를 쓰고, 단위 테스트는 fake 를
// 주입한다(seam) — 네트워크 의존 없이 배선 로직만 검증하기 위함.
type pvcUsageReader interface {
	// usagePercent 는 (percent, ok, err) 를 반환한다.
	// ok=false → 측정 불가(이번 reconcile 사이클 skip, 다음에 재시도).
	usagePercent(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (int32, bool, error)
}

// dbStatsUsageReader — 실 구현. RS 에 admin 자격으로 붙어 dbStats 의
// fsUsedSize/fsTotalSize 로 데이터 파일시스템 사용률을 계산한다. 기본 read
// preference(primary) 라 driver 가 dbStats 를 primary 로 라우팅한다.
type dbStatsUsageReader struct {
	c client.Client
}

func (rd *dbStatsUsageReader) usagePercent(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (int32, bool, error) {
	secret := &corev1.Secret{}
	secretName := mdb.Spec.Auth.AdminCredentialsSecretRef.Name
	if err := rd.c.Get(ctx, types.NamespacedName{Name: secretName, Namespace: mdb.Namespace}, secret); err != nil {
		return 0, false, fmt.Errorf("get admin secret %s: %w", secretName, err)
	}
	pw, ok := secret.Data["password"]
	if !ok {
		return 0, false, fmt.Errorf("password key not found in secret %s", secretName)
	}

	replicas := int(mdb.Spec.Members)
	if replicas < 1 {
		replicas = 1
	}
	hosts := mongodb.GetPodsFQDN(mdb.Name, mdb.Name+"-headless", mdb.Namespace, replicas, mongodPort)

	cli, err := mongodb.NewClient(ctx, mongodb.ConnectOpts{
		Hosts:      hosts,
		Username:   adminUserDB,
		Password:   string(pw),
		AuthDB:     adminUserDB,
		ReplicaSet: mdb.Name,
		Timeout:    8 * time.Second,
	})
	if err != nil {
		return 0, false, fmt.Errorf("connect for dbStats: %w", err)
	}
	defer func() { _ = cli.Disconnect(context.Background()) }()

	var res bson.M
	cmd := bson.D{{Key: "dbStats", Value: 1}, {Key: "scale", Value: 1}}
	if err := cli.Database(adminUserDB).RunCommand(ctx, cmd).Decode(&res); err != nil {
		return 0, false, fmt.Errorf("dbStats: %w", err)
	}
	used := bsonToFloat(res["fsUsedSize"])
	total := bsonToFloat(res["fsTotalSize"])
	if total <= 0 {
		return 0, false, fmt.Errorf("dbStats fsTotalSize<=0 (used=%v total=%v)", res["fsUsedSize"], res["fsTotalSize"])
	}
	pct := int32(used * 100 / total)
	return pct, true, nil
}

// bsonToFloat 는 bson numeric(int32/int64/double)을 float64 로 정규화한다.
// dbStats 의 fsUsedSize/fsTotalSize 는 mongod 버전/스케일에 따라 int64 또는
// double 로 올 수 있어 타입 스위치로 흡수한다.
func bsonToFloat(v any) float64 {
	switch n := v.(type) {
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	default:
		return 0
	}
}

// reconcilePVCAutoExpansion — spec.autoHealing.enabled 시 데이터 PVC 사용률을
// 측정해 임계 초과분을 온라인 증설한다. best-effort — 측정 실패/미배포는 skip 하고
// 다음 reconcile 에서 재시도한다(실패가 reconcile 을 막지 않는다).
func (r *MongoDBReconciler) reconcilePVCAutoExpansion(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) error {
	spec := mdb.Spec.AutoHealing
	if spec == nil || !spec.Enabled {
		return nil
	}
	logger := log.FromContext(ctx).WithName("pvc-autoexpand")

	currentGi, resizing, found, err := r.currentDataPVCSizeGi(ctx, mdb)
	if err != nil {
		return fmt.Errorf("read data PVC size: %w", err)
	}
	if !found {
		logger.V(1).Info("no data PVC found yet, skip auto-expansion")
		return nil
	}
	// 확장이 이미 진행 중이면(요청>용량 또는 resize 컨디션) 스킵 — 파일시스템이
	// 아직 커지지 않아 사용률이 높게 유지되는 창에서 중복 확장(runaway)을 차단한다.
	if resizing {
		logger.V(1).Info("data PVC resize in flight, skip auto-expansion this cycle")
		return nil
	}

	reader := r.PVCUsage
	if reader == nil {
		reader = &dbStatsUsageReader{c: r.Client}
	}
	pct, ok, err := reader.usagePercent(ctx, mdb)
	if err != nil || !ok {
		logger.V(1).Info("data volume usage unavailable, skip auto-expansion this cycle", "err", err)
		return nil
	}

	plan := PlanPVCExpansion(currentGi, pct, spec)
	if plan == nil {
		return nil // 임계 미만 — 확장 불필요.
	}

	desired := resource.MustParse(fmt.Sprintf("%dGi", plan.NewSizeGi))
	if err := commonspvc.ExpandDataPVCs(ctx, r.Client, mdb.Namespace, []string{mdb.Name}, desired); err != nil {
		return fmt.Errorf("auto-expand data PVCs: %w", err)
	}
	logger.Info("data PVC auto-expanded",
		"fromGi", plan.CurrentSizeGi, "toGi", plan.NewSizeGi, "usagePercent", pct)
	if r.Recorder != nil {
		r.Recorder.Eventf(mdb, nil, corev1.EventTypeNormal, "PVCAutoExpansion", "Expand",
			"데이터 PVC 자동 확장: %dGi→%dGi (사용률 %d%%)", plan.CurrentSizeGi, plan.NewSizeGi, pct)
	}
	return nil
}

// currentDataPVCSizeGi 는 데이터 PVC(data-<name>-N)의 최대 요청 용량(GiB, floor)과
// resize 진행 여부, 존재 여부를 반환한다. 실 PVC 를 SSOT 로 읽어(spec.storage.size 가
// 아님) 자동 확장이 단조 증가하도록 한다 — 이전 확장분 위에 증분을 쌓는다.
func (r *MongoDBReconciler) currentDataPVCSizeGi(ctx context.Context, mdb *mongodbv1alpha1.MongoDB) (currentGi int64, resizing, found bool, err error) {
	prefix := commonspvc.PVCNamePrefix(commonspvc.DefaultVCTName, mdb.Name) // "data-<name>-"
	list := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, list, client.InNamespace(mdb.Namespace)); err != nil {
		return 0, false, false, err
	}
	for i := range list.Items {
		p := &list.Items[i]
		if !strings.HasPrefix(p.Name, prefix) {
			continue
		}
		found = true
		req, ok := p.Spec.Resources.Requests[corev1.ResourceStorage]
		if ok {
			if gi := req.Value() >> 30; gi > currentGi { // bytes → GiB floor
				currentGi = gi
			}
			if capQ, hasCap := p.Status.Capacity[corev1.ResourceStorage]; hasCap && req.Cmp(capQ) > 0 {
				resizing = true // 요청 > 용량 = 볼륨 확장 대기 중
			}
		}
		for _, cond := range p.Status.Conditions {
			if cond.Type == corev1.PersistentVolumeClaimResizing ||
				cond.Type == corev1.PersistentVolumeClaimFileSystemResizePending {
				resizing = true
			}
		}
	}
	return currentGi, resizing, found, nil
}
