/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

// zone.go — Phase 5.3 sharded topology v2: zone-aware shard placement.
// mongos admin command sh.addShardToZone / sh.updateZoneKeyRange 의 operator wrapper.

package mongodb

import (
	"context"
	"fmt"
	"regexp"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var zoneNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,63}$`)

// AddShardToZoneCommand returns the admin command associating a shard with a zone.
func AddShardToZoneCommand(shardName, zone string) bson.D {
	return bson.D{{Key: "addShardToZone", Value: shardName}, {Key: "zone", Value: zone}}
}

// UpdateZoneKeyRangeCommand returns the admin command tagging a key range to a
// zone for a sharded namespace (ns="db.coll"). min/max are shard-key bound docs.
func UpdateZoneKeyRangeCommand(ns string, min, max bson.D, zone string) bson.D {
	return bson.D{
		{Key: "updateZoneKeyRange", Value: ns},
		{Key: "min", Value: min},
		{Key: "max", Value: max},
		{Key: "zone", Value: zone},
	}
}

// ValidateZoneName checks the zone name format (non-empty, alphanumeric/dash/
// underscore, max 63 chars). Empty or malformed is rejected.
func ValidateZoneName(zone string) error {
	if zone == "" {
		return fmt.Errorf("zone name 비어 있음")
	}
	if !zoneNameRe.MatchString(zone) {
		return fmt.Errorf("zone name %q: 영숫자/대시/언더스코어 1~63자 형식 아님", zone)
	}
	return nil
}

// AddShardToZone associates a shard with a zone via mongos (idempotent — already
// assigned is silent). Mirrors the connect+RunCommand pattern of the other
// ShardManager cluster operations.
func (s *ShardManager) AddShardToZone(ctx context.Context, mongosPod, namespace, shardName, zone string) error {
	if err := ValidateZoneName(zone); err != nil {
		return err
	}
	if shardName == "" {
		return fmt.Errorf("shardName 비어 있음")
	}
	c, err := s.connect(ctx, mongosPod, namespace, true)
	if err != nil {
		return fmt.Errorf("connect for AddShardToZone: %w", err)
	}
	defer disconnectQuiet(c)

	if err := c.Database(adminUserDB).RunCommand(ctx, AddShardToZoneCommand(shardName, zone)).Err(); err != nil {
		return fmt.Errorf("addShardToZone %s→%s: %w", shardName, zone, err)
	}
	return nil
}

// UpdateZoneKeyRange tags a shard-key range of a sharded namespace to a zone.
func (s *ShardManager) UpdateZoneKeyRange(ctx context.Context, mongosPod, namespace, ns string, min, max bson.D, zone string) error {
	if err := ValidateZoneName(zone); err != nil {
		return err
	}
	if ns == "" {
		return fmt.Errorf("ns (db.collection) 비어 있음")
	}
	c, err := s.connect(ctx, mongosPod, namespace, true)
	if err != nil {
		return fmt.Errorf("connect for UpdateZoneKeyRange: %w", err)
	}
	defer disconnectQuiet(c)

	if err := c.Database(adminUserDB).RunCommand(ctx, UpdateZoneKeyRangeCommand(ns, min, max, zone)).Err(); err != nil {
		return fmt.Errorf("updateZoneKeyRange %s→%s: %w", ns, zone, err)
	}
	return nil
}
