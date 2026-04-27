/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package mongodb

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRetryConfigPresets는 세 종류의 preset 설정이 의도한 범위 내 값을 반환하는지
// 확인한다. 각 preset의 의미는 운영 환경에서 reconcile 빈도와 직결되므로 회귀 시
// 즉시 감지되어야 한다.
func TestRetryConfigPresets(t *testing.T) {
	cases := []struct {
		name     string
		got      RetryConfig
		minSteps int
	}{
		{"default", DefaultRetryConfig(), 5},
		{"quick", QuickRetryConfig(), 3},
		{"long", LongRetryConfig(), 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got.InitialDelay <= 0 {
				t.Errorf("%s: InitialDelay must be > 0, got %v", tc.name, tc.got.InitialDelay)
			}
			if tc.got.MaxDelay < tc.got.InitialDelay {
				t.Errorf("%s: MaxDelay(%v) < InitialDelay(%v)", tc.name, tc.got.MaxDelay, tc.got.InitialDelay)
			}
			if tc.got.Factor <= 1.0 {
				t.Errorf("%s: Factor must be > 1.0, got %v", tc.name, tc.got.Factor)
			}
			if tc.got.MaxRetries < tc.minSteps {
				t.Errorf("%s: MaxRetries(%d) < expected min(%d)", tc.name, tc.got.MaxRetries, tc.minSteps)
			}
		})
	}
}

// TestRetryWithBackoff_SuccessFirstTry는 첫 시도에 성공하는 경우 fn이 정확히 1회만
// 호출되어야 함을 보장한다. 비싼 외부 호출(driver connect)이 retry로 중복되지
// 않도록 하기 위함.
func TestRetryWithBackoff_SuccessFirstTry(t *testing.T) {
	calls := 0
	cfg := RetryConfig{InitialDelay: time.Millisecond, MaxDelay: time.Millisecond, Factor: 2, MaxRetries: 3}
	err := RetryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

// TestRetryWithBackoff_SuccessAfterRetries는 N회 실패 후 성공하는 시나리오에서
// fn이 N+1번 호출되고 결국 nil을 반환하는지 확인한다.
func TestRetryWithBackoff_SuccessAfterRetries(t *testing.T) {
	calls := 0
	cfg := RetryConfig{InitialDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, Factor: 1.5, MaxRetries: 5}
	err := RetryWithBackoff(context.Background(), cfg, func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

// TestRetryWithBackoff_ExhaustedReturnsLastErr는 MaxRetries 소진 후 마지막 fn
// 에러가 그대로 반환되는지 확인한다. wait.ErrWaitTimeout 같은 wrapper가 새지
// 않아야 한다.
func TestRetryWithBackoff_ExhaustedReturnsLastErr(t *testing.T) {
	want := errors.New("永遠に失敗")
	cfg := RetryConfig{InitialDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond, Factor: 2, MaxRetries: 2}
	err := RetryWithBackoff(context.Background(), cfg, func() error { return want })
	if err == nil {
		t.Fatal("expected error after exhaustion")
	}
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

// TestRetryWithBackoff_ContextCanceled는 context가 취소되면 즉시 빠져나오는지
// 확인한다. 매니저 reconcile 컨텍스트가 끝나면 retry도 멈춰야 함.
func TestRetryWithBackoff_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := RetryConfig{InitialDelay: 10 * time.Millisecond, MaxDelay: 100 * time.Millisecond, Factor: 2, MaxRetries: 100}
	err := RetryWithBackoff(ctx, cfg, func() error { return errors.New("x") })
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}

// TestRetryUntilSuccess_FirstTry는 즉시 성공 시 ticker 대기 없이 반환되어야 함을
// 확인한다.
func TestRetryUntilSuccess_FirstTry(t *testing.T) {
	calls := 0
	err := RetryUntilSuccess(context.Background(), 100*time.Millisecond, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

// TestRetryUntilSuccess_RetriesUntilOK는 ticker tick 후 성공하는 경로를 검증.
func TestRetryUntilSuccess_RetriesUntilOK(t *testing.T) {
	calls := 0
	err := RetryUntilSuccess(context.Background(), time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls < 3 {
		t.Fatalf("expected at least 3 calls, got %d", calls)
	}
}

// TestRetryUntilSuccess_ContextCanceled는 ticker가 흐르는 동안 context가 만료되면
// ctx.Err()를 반환해야 함을 검증.
func TestRetryUntilSuccess_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := RetryUntilSuccess(ctx, time.Millisecond, func() error { return errors.New("永久に") })
	if err == nil {
		t.Fatal("expected context error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

// TestWaitForCondition_ImmediateTrue: condition이 첫 호출에 true 반환 시 ticker
// 대기 없이 nil 반환.
func TestWaitForCondition_ImmediateTrue(t *testing.T) {
	calls := 0
	err := WaitForCondition(context.Background(), 100*time.Millisecond, func() (bool, error) {
		calls++
		return true, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

// TestWaitForCondition_TrueAfterRetries: 처음엔 false, 나중에 true가 되는 시나리오.
func TestWaitForCondition_TrueAfterRetries(t *testing.T) {
	calls := 0
	err := WaitForCondition(context.Background(), time.Millisecond, func() (bool, error) {
		calls++
		return calls >= 3, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWaitForCondition_PropagatesError: condition이 에러 반환 시 즉시 그 에러를
// 그대로 반환. (재시도하지 않음)
func TestWaitForCondition_PropagatesError(t *testing.T) {
	want := errors.New("decode fail")
	err := WaitForCondition(context.Background(), time.Millisecond, func() (bool, error) {
		return false, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

// TestWaitForCondition_ContextCanceled: ctx 취소 시 ctx.Err() 반환.
func TestWaitForCondition_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := WaitForCondition(ctx, time.Millisecond, func() (bool, error) { return false, nil })
	if err == nil {
		t.Fatal("expected context error")
	}
}

// TestWaitForConditionWithBackoff_Success: backoff 모드에서도 true 반환 시 nil.
func TestWaitForConditionWithBackoff_Success(t *testing.T) {
	cfg := RetryConfig{InitialDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, Factor: 2, MaxRetries: 5}
	calls := 0
	err := WaitForConditionWithBackoff(context.Background(), cfg, func() (bool, error) {
		calls++
		return calls >= 2, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWithTimeout는 ctx의 deadline이 부모로부터 단축되었는지 확인.
func TestWithTimeout(t *testing.T) {
	parent := context.Background()
	ctx, cancel := WithTimeout(parent, 50*time.Millisecond)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	if time.Until(deadline) > 60*time.Millisecond {
		t.Fatalf("deadline too far: %v", time.Until(deadline))
	}
}

// TestWithDeadline은 명시 deadline이 그대로 전파되는지 확인.
func TestWithDeadline(t *testing.T) {
	want := time.Now().Add(100 * time.Millisecond)
	ctx, cancel := WithDeadline(context.Background(), want)
	defer cancel()
	got, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	if !got.Equal(want) {
		t.Fatalf("deadline mismatch: got %v, want %v", got, want)
	}
}
