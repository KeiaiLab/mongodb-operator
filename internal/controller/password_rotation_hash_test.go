package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TestHashSecretData_Deterministic 은 commons secrethash 위임으로 기존 map-순회
// 비결정성 버그가 교정됐음을 보증한다 — 동일 Secret 은 항상 동일 해시.
func TestHashSecretData_Deterministic(t *testing.T) {
	t.Parallel()
	s := &corev1.Secret{Data: map[string][]byte{
		"username": []byte("admin"),
		"password": []byte("p@ss"),
		"keyfile":  []byte("xyzkeyfilecontents"),
	}}
	first := hashSecretData(s)
	for range 200 {
		if got := hashSecretData(s); got != first {
			t.Fatalf("hashSecretData 비결정적: %s != %s", got, first)
		}
	}
	if len(first) != 16 {
		t.Fatalf("해시 길이 %d, want 16 (annotation 형식 truncation 보존)", len(first))
	}
}

// 값 변경이 해시에 반영돼야 rotation 신호가 동작한다.
func TestHashSecretData_ValueChangeChangesHash(t *testing.T) {
	t.Parallel()
	a := hashSecretData(&corev1.Secret{Data: map[string][]byte{"password": []byte("OLD")}})
	b := hashSecretData(&corev1.Secret{Data: map[string][]byte{"password": []byte("NEW")}})
	if a == b {
		t.Fatal("값 변경이 해시에 반영되지 않음")
	}
}
