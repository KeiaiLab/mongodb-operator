/*
Copyright 2024 Keiailab.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// updateStatusWithRetry는 주어진 객체의 status를 conflict 발생 시 retry하여 업데이트한다.
//
// 배경: r.Status().Update(ctx, obj)를 직접 호출하던 기존 패턴은 두 가지 약점이 있었다.
//  1. 한 reconcile 내에서 같은 객체를 여러 번 mutate한 뒤 마지막에 한 번만 업데이트하므로,
//     도중에 외부(다른 컨트롤러나 사용자)가 객체를 수정하면 Update가 conflict로 실패하고
//     reconcile이 처음부터 다시 시작된다. 무한 requeue 루프 가능.
//  2. mutate 함수 없이 호출하면 latest 상태를 fetch하지 않으므로, status field 외에
//     spec/metadata 변경이 손실될 위험이 있다.
//
// 이 헬퍼는 retry.RetryOnConflict로 conflict를 흡수한다. spec/metadata는 호출자의
// in-memory 사본을 신뢰(reconcile 컨텍스트의 source of truth가 보통 그것)하되,
// conflict 시 controller-runtime의 client.Status().Update가 ResourceVersion 충돌을
// 자동 감지하고 retry 콜백 안에서 다시 시도하도록 한다.
//
// 본 헬퍼는 status subresource만 다루므로 spec field 손실을 일으키지 않는다.
func updateStatusWithRetry(ctx context.Context, c client.Client, obj client.Object) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		err := c.Status().Update(ctx, obj)
		if err == nil {
			return nil
		}
		// conflict가 아닌 에러는 즉시 반환 (NotFound 등은 retry 의미 없음).
		if !apierrors.IsConflict(err) {
			return err
		}
		// conflict면 호출자가 보유한 ResourceVersion이 stale이므로 latest를 가져온다.
		// 이후 retry 콜백이 다시 호출될 때 Update가 새 ResourceVersion으로 시도된다.
		key := client.ObjectKeyFromObject(obj)
		if getErr := c.Get(ctx, key, obj); getErr != nil {
			// fetch 자체가 실패하면 원래 conflict 에러를 그대로 전파.
			return err
		}
		return err
	})
}
