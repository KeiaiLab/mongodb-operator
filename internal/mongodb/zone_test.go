/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package mongodb

import (
	"context"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestAddShardToZoneCommand(t *testing.T) {
	t.Parallel()
	got := AddShardToZoneCommand("rs-shard-0", "us-west")
	want := bson.D{{Key: "addShardToZone", Value: "rs-shard-0"}, {Key: "zone", Value: "us-west"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestUpdateZoneKeyRangeCommand(t *testing.T) {
	t.Parallel()
	min := bson.D{{Key: "uid", Value: bson.MinKey{}}}
	max := bson.D{{Key: "uid", Value: int64(1000)}}
	got := UpdateZoneKeyRangeCommand("app.users", min, max, "us-west")
	want := bson.D{
		{Key: "updateZoneKeyRange", Value: "app.users"},
		{Key: "min", Value: min},
		{Key: "max", Value: max},
		{Key: "zone", Value: "us-west"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestValidateZoneName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		zone    string
		wantErr bool
	}{
		{"정상 영숫자", "us-west-2", false},
		{"언더스코어", "zone_a", false},
		{"63자 경계", string(make([]byte, 0)) + "a123456789012345678901234567890123456789012345678901234567890123", false}, // 64? check below
		{"빈 문자열 invalid", "", true},
		{"공백 포함 invalid", "us west", true},
		{"슬래시 invalid", "us/west", true},
	}
	// 63자 정확히 (경계) 케이스 보정
	cases[2].zone = "a23456789012345678901234567890123456789012345678901234567890123" // 63 chars
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateZoneName(tc.zone)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateZoneName(%q) err=%v, wantErr=%v", tc.zone, err, tc.wantErr)
			}
		})
	}
}

func TestValidateZoneName_64CharsRejected(t *testing.T) {
	t.Parallel()
	z := "a234567890123456789012345678901234567890123456789012345678901234" // 64 chars
	if len(z) != 64 {
		t.Fatalf("테스트 셋업 오류: len=%d", len(z))
	}
	if err := ValidateZoneName(z); err == nil {
		t.Error("64자 zone name 은 거부 기대 (max 63)")
	}
}

func TestAddShardToZone_InvalidZoneRejectedBeforeConnect(t *testing.T) {
	t.Parallel()
	mgr := NewShardManagerWithFactory(failingFactory("connect 호출되면 안 됨"))
	err := mgr.AddShardToZone(context.Background(), "pod", "ns", "rs-shard-0", "bad zone")
	if err == nil {
		t.Fatal("invalid zone 이면 err 기대")
	}
	if err.Error() == "connect for AddShardToZone: connect 호출되면 안 됨" {
		t.Error("invalid zone 은 connect 전에 거부되어야 함")
	}
}

func TestAddShardToZone_EmptyShardNameRejected(t *testing.T) {
	t.Parallel()
	mgr := NewShardManagerWithFactory(failingFactory("nx"))
	if err := mgr.AddShardToZone(context.Background(), "pod", "ns", "", "us-west"); err == nil {
		t.Fatal("빈 shardName 거부 기대")
	}
}

func TestAddShardToZone_ConnectError(t *testing.T) {
	t.Parallel()
	mgr := NewShardManagerWithFactory(failingFactory("dial fail"))
	if err := mgr.AddShardToZone(context.Background(), "pod", "ns", "rs-shard-0", "us-west"); err == nil {
		t.Fatal("connect 실패 시 err 기대")
	}
}

func TestUpdateZoneKeyRange_EmptyNsRejected(t *testing.T) {
	t.Parallel()
	mgr := NewShardManagerWithFactory(failingFactory("nx"))
	if err := mgr.UpdateZoneKeyRange(context.Background(), "pod", "ns", "", bson.D{}, bson.D{}, "us-west"); err == nil {
		t.Fatal("빈 ns 거부 기대")
	}
}
