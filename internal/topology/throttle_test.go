/*
Copyright 2026 Keiailab.

SPDX-License-Identifier: MIT
*/

package topology

import (
	"testing"
	"time"
)

func TestComputeMigrationThrottle_ZeroRemainingStops(t *testing.T) {
	plan := ComputeMigrationThrottle(ThrottleInput{RemainingChunks: 0})
	if plan.MaxConcurrentMigrations != 0 {
		t.Fatalf("잔여 0 이면 migration 0 기대, got %d", plan.MaxConcurrentMigrations)
	}
}

func TestComputeMigrationThrottle_HighCPUBacksOff(t *testing.T) {
	plan := ComputeMigrationThrottle(ThrottleInput{RemainingChunks: 100, MongosCPUUtil: 0.81, LastPollInterval: 30 * time.Second})
	if plan.MaxConcurrentMigrations != 1 {
		t.Fatalf("高 CPU 면 migration 1 제한 기대, got %d", plan.MaxConcurrentMigrations)
	}
	if plan.PollInterval != 5*time.Minute {
		t.Errorf("高 CPU 면 최대 backoff(5m) 기대, got %v", plan.PollInterval)
	}
}

func TestComputeMigrationThrottle_CPUBoundary(t *testing.T) {
	// >0.80 만 高. 0.80 은 高 아님, 0.81 은 高.
	at := ComputeMigrationThrottle(ThrottleInput{RemainingChunks: 10, MongosCPUUtil: 0.80, LastPollInterval: 30 * time.Second})
	if at.MaxConcurrentMigrations == 1 {
		t.Errorf("CPU=0.80 은 高 아님 기대(>0.80 만 高)")
	}
	over := ComputeMigrationThrottle(ThrottleInput{RemainingChunks: 10, MongosCPUUtil: 0.81, LastPollInterval: 30 * time.Second})
	if over.MaxConcurrentMigrations != 1 {
		t.Errorf("CPU=0.81 은 高 기대")
	}
}

func TestComputeMigrationThrottle_LowCPUShortInterval(t *testing.T) {
	plan := ComputeMigrationThrottle(ThrottleInput{RemainingChunks: 100, MongosCPUUtil: 0.2, LastPollInterval: 0})
	if plan.PollInterval >= 5*time.Minute {
		t.Errorf("저부하 첫 호출은 짧은 interval 기대, got %v", plan.PollInterval)
	}
	if plan.MaxConcurrentMigrations < 1 {
		t.Errorf("저부하 + 잔여 chunk 면 migration >=1 기대")
	}
}

func TestComputeMigrationThrottle_BackoffMonotonic(t *testing.T) {
	low := ComputeMigrationThrottle(ThrottleInput{RemainingChunks: 100, MongosCPUUtil: 0.2, LastPollInterval: 30 * time.Second})
	high := ComputeMigrationThrottle(ThrottleInput{RemainingChunks: 100, MongosCPUUtil: 0.2, LastPollInterval: 2 * time.Minute})
	if high.PollInterval < low.PollInterval {
		t.Fatalf("LastPollInterval 증가 시 PollInterval 단조 비감소 기대: low=%v high=%v", low.PollInterval, high.PollInterval)
	}
}
