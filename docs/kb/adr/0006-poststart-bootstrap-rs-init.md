# ADR-0006: postStart hook 안에서 RS init + createUser 통합

- Date: 2026-04-29
- Status: Accepted
- Authors: @eightynine01

## Context

`MongoDB`/`MongoDBSharded` CR이 적용되면 operator는 STS를 만들고 mongod이
`--auth + --keyFile` 모드로 시작된다. 이 시점부터 *부트스트랩 deadlock*이
발생한다.

- mongo의 *localhost-exception*은 첫 `createUser`에만 작동하고
  `replSetInitiate`에는 작동하지 않는다(MongoDB 공식 문서).
- 그러나 *pod 내부 localhost connect*에서는 0-user 윈도에서 두 명령이 모두
  허용되며, mongod의 SCRAM 핸드셰이크는 외부 IP에 대해서만 강제된다.
- operator는 외부 IP(headless service FQDN)로 connect하므로 `replSetInitiate`
  자체가 `Unauthorized(13)`로 거부된다 → RS init 못 함 → user 생성 못 함 →
  영구 stuck.

이전 사이클(v1.1.1)의 `postStart` hook은 createUser만 시도했고 RS init은
operator가 외부 connect로 맡았다. 그 결과 본 클러스터의 fresh CR에서:

```
exec sh-verify-cfg-0 -- mongosh --host 127.0.0.1 --port 27019 --eval rs.initiate(...)
  → INITIATE_OK {"ok":1}        (pod 내부 localhost — 정상)

operator → sh-verify-cfg-0.<headless>...:27019 replSetInitiate
  → Unauthorized                (외부 connect — 거부)
```

이 실측은 *외부 connect 부트스트랩이 mongo의 보안 모델로 인해 본질적으로
불가능*함을 입증한다.

## Decision

`buildAdminBootstrapScript`(=postStart hook 스크립트)가 RS init과 createUser를
*함께 처리*하도록 통합한다.

```bash
ORDINAL="${HOSTNAME##*-}"
[ "$ORDINAL" != "0" ] && exit 0      # ordinal-0만 RS init

# 1. mongod ping 대기 (최대 120초)
# 2. rs.status() — NotYetInitialized(94)이면 rs.initiate(...)
# 3. PRIMARY 대기 (writable=true)
# 4. createUser (UserAlreadyExists 멱등 처리)
```

STS env로 동적 정보 주입:
- `MONGO_PORT` (RS=27017 / cfg=27019 / shard=27018)
- `MONGO_REPLSET` (RS 식별자)
- `MONGO_MEMBERS` (콤마 list of `<pod-FQDN>:<port>`)
- `MONGO_CONFIGSVR` ("true"이면 `rs.initiate(cfg.configsvr=true)`)

operator의 책임 변화:
- **이전**: 외부 connect로 RS init + createUser 둘 다 시도 (실패).
- **이후**: status 추적 + admin user verify + sharded의 `addShard` 등 *RS
  외부에서 의미 있는 명령*만 처리.

mongos는 Deployment + ClusterIP 구조라 `<pod-name>.<svc>...` DNS가 안 잡힌다.
sharded controller는 별도의 `NewServiceConnectFactory`로 mongos service DNS
(`<svc>.<ns>.svc.cluster.local:27017`)로 connect. mongos의 postStart hook은
ordinal != 0(deploy hash가 끝에 옴)이라 RS init 분기를 자동 skip → 자동 무해.

## Consequences

긍정:
- 부트스트랩이 *사용자 개입 없이* 자동 완료. 본 사이클 실 측정:
  - RS `rs-auto`: CR 적용 → Phase=Running 67초.
  - Sharded `sh-auto`(cfg×3 + shard×3 + mongos×1): Phase=Running 152초.
- 외부 RS init / 외부 createUser deadlock이 *원리적으로* 사라진다(localhost-
  exception을 본래 의도대로 활용).
- mongos는 hook 자동 skip으로 별도 코드 없이 정상 startup.

부정:
- ConfigMap script(postStart 스크립트)가 STS의 일급 의존성이 됨. 스크립트
  실패는 readiness 미달 → reconcile 가시화로 진단 가능, 단 디버그 시
  `kubectl logs <pod> --previous`가 아닌 `kubectl describe pod` events에서
  PostStartHook failure를 봐야 한다.
- ordinal-0이 아닌 멤버는 *RS init 시도조차 안 함*. ordinal-0 pod이 영구
  스케줄 실패하면 RS init이 일어나지 않는다. 단, StatefulSet은 ordinal-0을
  먼저 ready로 만드는 PodManagementPolicy=OrderedReady가 default(우리 cfg는
  Parallel이지만 ordinal-0이 ready되지 않으면 RS는 어차피 quorum 부족).
- env에 `MONGO_MEMBERS`로 모든 멤버 FQDN을 *고정 list*로 박아둔다. 멤버 수
  변경 시 STS rolling update + 재기동이 필요. 본 fix는 *부트스트랩 단계*만을
  목표로 하며, replicas 조정은 operator의 RS reconfig 경로(이미 존재)가 처리.

후속 작업:
- HPA 신규 기능(D 결함) — operator CR spec + reconcile 로직 추가가 필요한
  별도 사이클.
- postStart 스크립트의 단위 테스트 — bash + mongosh mock으로 ordinal 분기,
  configsvr 플래그, idempotency 케이스 회귀 가드(현재는 통합 검증으로 대체).

## Alternatives Considered

1. **operator가 K8s API exec로 pod 내부 mongosh 실행**: 외부 connect 한계를
   우회 가능하지만 controller-runtime 표준 패턴이 아니며, RBAC에 `pods/exec`
   추가가 필요하고 권한 표면이 넓어진다. 거절.
2. **mongod을 처음에는 `--auth` 없이 시작 → 부트스트랩 후 STS args 변경 + 재
   기동**: rolling restart가 필요하고, restart 직후 transient unauth 윈도에
   client connect가 들어오면 *인증 없이 통과*되는 보안 위험. 거절.
3. **mongo의 `--clusterAuthMode keyFile`만 + `--auth` 제거**: keyfile은
   internal cluster 인증에만 사용되고 client connect에는 인증 없이 허용 →
   보안 hole. 거절.
4. **operator를 host network로 띄우고 직접 IP connect**: cluster API 우회는
   가능하지만 mongod connect는 여전히 외부 IP 인증 거부. 본 결함과 무관. 거절.
