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

func TestBalancerWindowUpdate(t *testing.T) {
	t.Parallel()
	t.Run("둘 다 비면 activeWindow $unset", func(t *testing.T) {
		got := BalancerWindowUpdate("", "")
		want := bson.D{{Key: "$unset", Value: bson.D{{Key: "activeWindow", Value: ""}}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("start+stop 면 activeWindow $set", func(t *testing.T) {
		got := BalancerWindowUpdate("23:00", "06:00")
		want := bson.D{{Key: "$set", Value: bson.D{{Key: "activeWindow", Value: bson.D{
			{Key: "start", Value: "23:00"}, {Key: "stop", Value: "06:00"},
		}}}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestValidateBalancerWindow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		start, stop string
		wantErr     bool
	}{
		{"둘 다 비면 valid (window 없음)", "", "", false},
		{"정상 HH:MM", "23:00", "06:30", false},
		{"경계 00:00 / 23:59", "00:00", "23:59", false},
		{"start 만 비면 invalid", "", "06:00", true},
		{"stop 만 비면 invalid", "23:00", "", true},
		{"start 24:00 범위초과", "24:00", "06:00", true},
		{"stop 분 60 범위초과", "23:00", "06:60", true},
		{"형식 깨짐", "2300", "06:00", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateBalancerWindow(tc.start, tc.stop)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateBalancerWindow(%q,%q) err=%v, wantErr=%v", tc.start, tc.stop, err, tc.wantErr)
			}
		})
	}
}

func TestSetBalancerWindow_InvalidWindowRejectedBeforeConnect(t *testing.T) {
	t.Parallel()
	// connect 가 호출되면 실패하는 factory — invalid window 면 connect 전에 반환되어야 함.
	mgr := NewShardManagerWithFactory(failingFactory("connect 호출되면 안 됨"))
	err := mgr.SetBalancerWindow(context.Background(), "pod", "ns", "23:00", "") // stop 누락 = invalid
	if err == nil {
		t.Fatal("invalid window 면 err 기대")
	}
	if err.Error() == "connect for SetBalancerWindow: connect 호출되면 안 됨" {
		t.Error("invalid window 는 connect 전에 거부되어야 함 (connect 호출됨)")
	}
}

func TestSetBalancerWindow_ConnectError(t *testing.T) {
	t.Parallel()
	mgr := NewShardManagerWithFactory(failingFactory("dial fail"))
	err := mgr.SetBalancerWindow(context.Background(), "pod", "ns", "23:00", "06:00")
	if err == nil {
		t.Fatal("connect 실패 시 err 기대")
	}
}
