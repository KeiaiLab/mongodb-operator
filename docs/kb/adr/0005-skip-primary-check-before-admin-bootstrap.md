# ADR-0005: admin user 부트스트랩 전 primary 체크 (anon + auth-fallback)

- Date: 2026-04-29
- Status: Accepted
- Authors: @eightynine01

## Context

reconcile flow의 step 8(`hasPrimary`)은 RS init 직후 primary 선출을 기다리는
역할이다. step 9의 `reconcileAdminUser`가 *primary가 살아있는 상태에서만*
의미가 있다는 가정에 기반했다.

`hasPrimary`는 `Status.AdminUserCreated`를 분기 키로 사용한다
(`mongodb_controller.go:367`). false면 익명 매니저로, true면 인증 매니저로
`replSetGetStatus`를 호출한다.

문제는 `Status.AdminUserCreated=false`이면서 RS가 이미 auth-on인 상태에서
발생한다(ADR-0004와 동일 패턴 — 부트스트랩 중단, 외부 init, postStart hook
선행). 익명 `replSetGetStatus`는 Unauthorized로 거부되고, controller는
PrimaryUnreachable condition을 기록한 뒤 단순 requeue. step 9의 admin user
부트스트랩에 영원히 도달하지 못한다.

ADR-0004는 *RS init 체크* 분기에서 같은 패턴을 init-completed 시그널로
해석하도록 수정했다. 그러나 step 8(primary 체크)에는 다른 의미적 선택지가
있다.

## Decision

`Status.AdminUserCreated=false` 단계에서는 step 8을 *익명 매니저로 부분 부활*
한다. 분기는 다음 4-way switch:

| 응답 | 의미 | 동작 |
|---|---|---|
| `(true, nil)` | RS auth-off + primary 선출됨 | 부트스트랩 진행 |
| `(false, nil)` | RS auth-off + 정상 미선출(election 진행) | 5s requeue |
| `(_, IsAuthRequiredErr)` | RS auth-on, primary 모름 | 부트스트랩 진행 (driver server selection이 처리) |
| `(_, other err)` | connect/network 실패 | PrimaryUnreachable condition + 10s requeue |

근거 — fresh RS의 정상 election 대기를 step 8이 명시적으로 처리해야 한다.
이를 *완전 skip*하면 election 대기가 `BootstrapAdminUser`의 driver server
selection timeout에 의존하게 되어, election이 default 10s를 넘어 길어지면
`Type: RSGhost / Type: Unknown` topology에 대해 server selection error로
실패한다. 익명 hasPrimary로 election 완료를 먼저 확인한 뒤 부트스트랩으로
진행하는 것이 의미적으로도 정확하고, fresh RS와 drift RS 두 시나리오를 동일
분기로 처리한다.

`BootstrapAdminUser`는 동시에 `Timeout: 25s`로 server selection 마진을
확장한다 — 정상 election 후에도 driver topology 안정화 잠깐의 갭을 흡수.

## Consequences

긍정:
- ADR-0004와 함께 부트스트랩 중단 → 자동 재개 경로를 완성한다.
- 익명 매니저로 auth-on RS의 status를 읽어야 한다는 모순적 요구가 사라진다.
- 부트스트랩 단계에서 PrimaryUnreachable condition이 잘못 기록되는 일이
  사라진다(11h stuck 시나리오의 가시 증상).

부정:
- step 9의 `reconcileAdminUser`는 driver가 primary를 찾을 때까지 자체
  timeout(server selection timeout, 기본 30s)을 사용한다. primary가 끝내
  선출되지 않으면 reconcileAdminUser가 timeout으로 fail → updateStatusError
  로 명확한 에러 표면화. 본 변경 전에는 step 8에서 단순 requeue로 무한
  대기였으므로 *진단성 향상*에 가까운 부정적 측면.
- 만약 RS가 비정상이라 primary가 영구 부재면, 부트스트랩 시도가 매번 server
  selection timeout(~30s)을 소모한다. 정상 RS에서는 ms 단위로 통과하므로
  실용적 부담은 작다.

후속 작업:
- 별도 P1: `reconcileAdminUser`에 짧은 server selection timeout(예 10s)을
  명시 적용해 비정상 RS에서 reconcile 한 사이클이 너무 오래 걸리지 않도록.

## Alternatives Considered

1. **`hasPrimary` 자체에 ADR-0004와 같은 Unauthorized 분기 추가**: 익명
   매니저가 Unauthorized면 `(true, nil)` 반환. *의미적으로 잘못됨* — 매니저
   가 primary 존재 여부를 모르는 상태인데 "있다"고 단정한다. 거절.
2. **익명 매니저를 제거하고 첫 부트스트랩에 fallback authentication을 사용**:
   부트스트랩 전엔 인증 정보가 의미 없으므로 fallback 자체가 형용 모순.
   거절.
3. **`AdminUserCreated=false` + RS init=true 상태에서 별도 transient phase
   (`Bootstrapping`)로 전이**: phase 다양화는 사용자 가시성 향상이지만
   현 의사결정 범위 밖. 후속 사이클에서 검토.
