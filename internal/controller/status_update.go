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
//  2. conflict 시 latest를 refetch하면 호출자가 obj에 설정해 둔 status mutation이
//     서버 상태로 덮어쓰여 silent하게 유실된다 (mutate-after-get 부재).
//
// 이 헬퍼는 retry.RetryOnConflict로 conflict를 흡수한다. 정상 경로(conflict 없음)는
// 호출자의 in-memory 사본을 그대로 영속화한다. conflict 발생 시에는 최신 ResourceVersion으로
// refetch한 뒤, 가변 인자로 받은 mutate 콜백을 다시 적용해 호출자의 status 변경을 잃지 않고
// 재Update한다. mutate 콜백을 넘기지 않은 기존 호출자는 종전 동작(refetch 후 단순 재시도)을
// 그대로 유지하므로 점진 마이그레이션이 가능하다.
//
// 본 헬퍼는 status subresource만 다루므로 spec field 손실을 일으키지 않는다.
func updateStatusWithRetry(ctx context.Context, c client.Client, obj client.Object, mutate ...func()) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := c.Status().Update(ctx, obj); err == nil {
			return nil
		} else if !apierrors.IsConflict(err) {
			// conflict가 아닌 에러는 즉시 반환 (NotFound 등은 retry 의미 없음).
			return err
		}
		// conflict: 호출자가 보유한 ResourceVersion이 stale이므로 최신본으로 refetch한다.
		if getErr := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); getErr != nil {
			// fetch 자체가 실패하면 해당 에러를 전파.
			return getErr
		}
		// refetch가 호출자의 status 변경을 덮어썼으므로 mutate 콜백으로 재적용 후 다시 Update한다.
		// 콜백이 없으면(기존 호출자) 갱신된 ResourceVersion으로 한 번 더 재시도한다.
		for _, m := range mutate {
			m()
		}
		return c.Status().Update(ctx, obj)
	})
}
