# ADR-0004: `IsInitialized` Unauthorized 응답을 init-completed 시그널로 해석

- Date: 2026-04-29
- Status: Accepted
- Authors: @eightynine01

## Context

`reconcileReplicaSetInitialization`은 RS init 여부를 판단하기 위해 *익명* 매니저
(`newAnonRSManager`)로 `replSetGetStatus`를 호출한다. 익명을 사용하는 근거는
ADR-0002의 부트스트랩 모델 — RS init과 admin user 생성 사이의 좁은 창에서
mongod이 `--auth+--replSet`으로 떠 있지만 user는 0명이라 익명 접근이 허용되는
유일한 시점이기 때문.

그러나 다음 운영 시나리오에서 이 가정이 깨진다.

1. **부트스트랩 중단** — operator가 RS init까지 마쳤으나 admin user 생성에
   실패해 `Status.AdminUserCreated=false`인 채로 종료. 다음 reconcile은 같은
   분기를 다시 시도하지만, 그 사이 mongod의 *postStart hook*이 admin user를
   생성했다면 RS는 이미 auth-on 상태로 진입했다(0-user 윈도가 닫힘).
2. **외부 RS init** — operator가 인수한 기존 RS가 이미 admin user를 가진 채
   auth-on으로 운영 중인 시나리오.

두 경우 모두 익명 `replSetGetStatus`는 `Unauthorized(13)` 또는
`AuthenticationFailed(18)`로 거부된다. 이전 구현은 이 응답을 generic command
error로 propagate했고, 결과적으로 `Status.Phase=Failed`로 영구 stuck —
admin user 부트스트랩 분기에 도달하지 못했다.

INC-0001(2026-04-29)는 이 패턴이 실제 클러스터에서 11시간 동안 미복구로
방치된 사례다.

## Decision

`replSetGetStatus`의 응답을 다음 규칙으로 분류한다.

| Server error code | 의미 | `IsInitialized` 반환 |
|---|---|---|
| `94` (NotYetInitialized) | RS 미초기화 | `(false, nil)` |
| `13` (Unauthorized) | RS 초기화 완료 + auth 활성 | `(true, nil)` |
| `18` (AuthenticationFailed) | 동상 | `(true, nil)` |
| 그 외 | 분류 불가 | `(false, error)` propagate |

이 분류는 `classifyReplSetGetStatusErr` 순수 함수로 추출해 단위 테스트로
회귀 가드한다.

근거:
- 코드 18(SCRAM 인증 실패)이 발생하려면 mongod이 auth 모드로 동작하고 user
  db가 형성돼야 한다. user db는 RS init 후에만 의미가 있으므로 init은 이미
  완료된 상태로 단정 가능하다.
- 코드 13은 권한 부족 — auth가 켜져 있어야 발생한다. RS init 전에는
  auth가 의미 없으므로(replSetInitiate 자체가 RS state를 만든다) init 완료
  시그널로 안전.

## Consequences

긍정:
- 부트스트랩 중단 → 재개 경로가 자동화된다. operator가 admin user 부트스트랩
  분기로 진입해 멱등성 가드(`isUserAlreadyExistsErr`/`isAuthRequiredErr`)로
  case-A(0-user, localhost-exception 활성)와 case-B(user 이미 존재) 모두
  정상 처리.
- 외부 RS 인수 시나리오를 zero-config로 지원.

부정:
- mongod 빌드/구성에 따라 코드 13/18을 다르게 반환할 가능성. 예: 향후 mongo
  버전이 코드 변경 시 분류 실패 → propagate으로 fall-through. 단위 테스트가
  unknown code → not decided 분기를 가드하므로 silent break는 없다.
- "auth 활성 = RS init 완료"가 *항상* 참인지 — auth 활성에 init이 필수라는
  mongod 구현 보장이 없다면 false positive 가능. 하지만 실용적으로 RS init
  전에는 auth가 무의미해 mongod 자체가 0-user 익명 접근을 허용한다.

후속 작업:
- `BootstrapAdminUser`도 대응 변경(ADR-0004의 스코프) — 직접 primary 추적을
  driver 자동 server selection으로 위임해 0-user/auth-active 두 상태 모두
  멱등 createUser로 수렴.
- INC-0001에 본 ADR을 `Refs:`로 연결.

## Alternatives Considered

1. **`Status.Phase=Failed` 상태에서 strong-reset 분기 추가**: 부트스트랩이
   특정 시간 이상 정체되면 STS/PVC를 폐기하고 재생성. 데이터 손실 위험과
   사용자 신뢰 하락이 너무 크다. 거절.
2. **`Status.AdminUserCreated=false` + RS init 후 시도 횟수 임계 → 사용자
   알림 + 정지**: 안전하지만 복구가 운영자 수작업으로만 가능. 자동 복구
   가능한 케이스를 사람 손에 맡기는 것은 OSS 사용성 면에서 후퇴. 거절.
3. **익명 매니저를 제거하고 항상 인증 매니저로 시도**: 0-user 윈도에서 인증
   매니저가 SCRAM 핸드셰이크를 시도하면 안 한 것만 못한 결과. RS init 직후
   자체가 깨진다. 거절.
