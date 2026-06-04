/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// balancer.go — Phase 5.3 sharded topology v2: chunk migration throttling.
// MongoDB balancer active window (config.settings) — 청크 마이그레이션을 지정
// 시간창으로 제한해 피크 시간대 부하를 회피한다.

package mongodb

import (
	"context"
	"fmt"
	"regexp"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const balancerSettingsID = "balancer"

var hhmmRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// BalancerSettingsFilter is the config.settings selector for the balancer doc.
func BalancerSettingsFilter() bson.D {
	return bson.D{{Key: "_id", Value: balancerSettingsID}}
}

// BalancerWindowUpdate returns the config.settings update applying the balancer
// active window [start, stop) (HH:MM). When both are empty the window is removed
// ($unset) so the balancer may run at any time.
func BalancerWindowUpdate(start, stop string) bson.D {
	if start == "" && stop == "" {
		return bson.D{{Key: "$unset", Value: bson.D{{Key: "activeWindow", Value: ""}}}}
	}
	return bson.D{{Key: "$set", Value: bson.D{{Key: "activeWindow", Value: bson.D{
		{Key: "start", Value: start},
		{Key: "stop", Value: stop},
	}}}}}
}

// ValidateBalancerWindow checks HH:MM (24h) format. Both empty = valid (no
// window). Exactly one empty, or a malformed time, is rejected.
func ValidateBalancerWindow(start, stop string) error {
	if start == "" && stop == "" {
		return nil
	}
	if start == "" || stop == "" {
		return fmt.Errorf("balancer window: start와 stop 둘 다 지정하거나 둘 다 비워야 함 (start=%q stop=%q)", start, stop)
	}
	if !hhmmRe.MatchString(start) {
		return fmt.Errorf("balancer window start %q: HH:MM (24h) 형식 아님", start)
	}
	if !hhmmRe.MatchString(stop) {
		return fmt.Errorf("balancer window stop %q: HH:MM (24h) 형식 아님", stop)
	}
	return nil
}

// SetBalancerWindow upserts the balancer active window into config.settings via
// mongos. Empty start/stop removes the window. Mirrors the connect+RunCommand
// pattern of the other ShardManager cluster operations.
func (s *ShardManager) SetBalancerWindow(ctx context.Context, mongosPod, namespace, start, stop string) error {
	if err := ValidateBalancerWindow(start, stop); err != nil {
		return err
	}
	c, err := s.connect(ctx, mongosPod, namespace, true)
	if err != nil {
		return fmt.Errorf("connect for SetBalancerWindow: %w", err)
	}
	defer disconnectQuiet(c)

	_, err = c.Database("config").Collection("settings").UpdateOne(
		ctx, BalancerSettingsFilter(), BalancerWindowUpdate(start, stop),
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("set balancer window: %w", err)
	}
	return nil
}
