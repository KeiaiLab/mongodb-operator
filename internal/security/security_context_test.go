/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package security

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func boolPtr(b bool) *bool    { return &b }
func int64Ptr(i int64) *int64 { return &i }

func TestDefaultPodSecurityContext_NonRoot999(t *testing.T) {
	sc := DefaultPodSecurityContext()
	if sc == nil {
		t.Fatal("nil 아닌 PodSecurityContext 기대")
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != mongoUserGroup {
		t.Errorf("RunAsUser=%d 기대, got %v", mongoUserGroup, sc.RunAsUser)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Errorf("RunAsNonRoot=true 기대, got %v", sc.RunAsNonRoot)
	}
}

func TestDefaultContainerSecurityContext_NonRoot999(t *testing.T) {
	sc := DefaultContainerSecurityContext()
	if sc == nil {
		t.Fatal("nil 아닌 SecurityContext 기대")
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != mongoUserGroup {
		t.Errorf("RunAsUser=%d 기대, got %v", mongoUserGroup, sc.RunAsUser)
	}
}

func TestKeyfileInitSecurityContext_NonNil(t *testing.T) {
	if KeyfileInitSecurityContext() == nil {
		t.Error("nil 아닌 SecurityContext 기대")
	}
}

func TestPreflightContainerSecurityContext_NilReturnsNil(t *testing.T) {
	if f := PreflightContainerSecurityContext(nil); f != nil {
		t.Errorf("nil sc → nil findings 기대, got %v", f)
	}
}

func TestPreflightContainerSecurityContext_CleanReturnsNone(t *testing.T) {
	sc := &corev1.SecurityContext{RunAsNonRoot: boolPtr(true), RunAsUser: int64Ptr(999)}
	if f := PreflightContainerSecurityContext(sc); len(f) != 0 {
		t.Errorf("clean sc → 0 findings 기대, got %v", f)
	}
}

func TestPreflightContainerSecurityContext_PrivilegedIsError(t *testing.T) {
	f := PreflightContainerSecurityContext(&corev1.SecurityContext{Privileged: boolPtr(true)})
	if len(f) != 1 || f[0].Severity != SeverityError {
		t.Fatalf("privileged → 1 Error finding 기대, got %v", f)
	}
}

func TestPreflightContainerSecurityContext_RunAsRootIsError(t *testing.T) {
	f := PreflightContainerSecurityContext(&corev1.SecurityContext{RunAsUser: int64Ptr(0)})
	if len(f) != 1 || f[0].Severity != SeverityError {
		t.Fatalf("runAsUser=0 → 1 Error finding 기대, got %v", f)
	}
}

func TestPreflightContainerSecurityContext_RunAsNonRootFalseIsWarning(t *testing.T) {
	f := PreflightContainerSecurityContext(&corev1.SecurityContext{RunAsNonRoot: boolPtr(false)})
	if len(f) != 1 || f[0].Severity != SeverityWarning {
		t.Fatalf("runAsNonRoot=false → 1 Warning finding 기대, got %v", f)
	}
}
