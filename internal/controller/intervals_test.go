/*
Copyright 2026 Keiailab.
*/

package controller

import (
	"testing"
	"time"
)

func TestRequeueCadencePreservesExistingDurations(t *testing.T) {
	t.Parallel()

	if requeueSteady != 30*time.Second {
		t.Fatalf("requeueSteady: want 30s, got %s", requeueSteady)
	}
	if requeueProvisioning != 10*time.Second {
		t.Fatalf("requeueProvisioning: want 10s, got %s", requeueProvisioning)
	}
	if requeueWaitForExternal != 5*time.Second {
		t.Fatalf("requeueWaitForExternal: want 5s, got %s", requeueWaitForExternal)
	}
}
