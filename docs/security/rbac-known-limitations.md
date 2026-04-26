# RBAC 알려진 한계: pods/exec 권한 (C6)

## 요약

`mongodb-operator`의 controller-manager ServiceAccount는 ClusterRole을 통해
`core/pods/exec` 리소스에 대한 `create` 권한을 가진다. 이는 클러스터의 임의
Pod에서 임의 명령을 exec할 수 있는 강한 권한이다. 본 문서는 이 권한이 왜
현재 필요한지, 어떤 위험을 동반하는지, 그리고 어떤 경로로 제거할 계획인지를
명시한다.

## 현재 상태

- 마커 위치: `internal/controller/mongodb_controller.go`,
  `internal/controller/mongodbsharded_controller.go`
- 생성 결과: `config/rbac/role.yaml`의 ClusterRole `manager-role`에
  `pods/exec` `create` 규칙
- verbs: `create`만 부여 (delete/update 등 없음, 최소 권한)

## 왜 필요한가

`internal/mongodb/` 패키지의 모든 MongoDB 명령(`rs.initiate`, `sh.addShard`,
`createUser`, `db.auth` 등)은 mongosh를 대상 Pod 안에서 실행하는 방식으로
수행된다. 이는 다음을 위해 `pods/exec` 권한을 요구한다:

- replica set 초기화는 첫 멤버 Pod에서 직접 `rs.initiate(...)` 호출이 필요
- sharded cluster의 mongos 라우팅 설정은 mongos Pod 안에서 `sh.addShard(...)` 필요
- 인증 설정 전 단계에서는 외부 네트워크 클라이언트로 접근 불가
  (auth가 활성화되기 전이므로 localhost exception 필요)

## 왜 좁히기 어려운가

- **resourceNames**: pods/exec은 resourceNames 제한을 지원하지만 mongodb Pod
  이름이 사용자 지정 CR 이름 + 동적 ordinal(`<name>-0`, `<name>-1`, ...)에
  의존하므로 사전 정의 불가.
- **namespace scope**: operator는 cluster-wide watch이므로 namespace-scoped
  Role + 동적 RoleBinding 패턴은 watch namespace를 추적하기 위한 추가 컴포넌트가
  필요하고, 결국 cluster-scoped 권한과 동등한 권한을 분산해서 가지게 된다.
- **verbs**: 이미 `create`만 부여. `pods/exec`의 다른 verb는 의미가 없다.

## 위험

`pods/exec`을 가진 ServiceAccount의 토큰을 탈취하면, 공격자는 클러스터의
모든 namespace의 모든 Pod에서 임의 명령을 실행할 수 있다. 즉 operator Pod이
침해되면 클러스터 전체가 침해된다고 봐야 한다.

완화 조치 (현재 적용됨):
- mongosh 명령어 인젝션 차단 (커밋 `85e6392`)
- 자격증명을 stdin script로 전달해 audit log/process listing 노출 차단 (`4a6fa42`)
- securityContext: runAsNonRoot, readOnlyRootFilesystem, drop ALL capabilities
  (`config/manager/manager.yaml`)

## 제거 경로

근본 해결은 **MongoDB 공식 Go 드라이버(`go.mongodb.org/mongo-driver`)**를
도입해 mongosh exec을 모두 네트워크 클라이언트로 대체하는 것이다. 이 작업이
완료되면 `pods/exec` 권한과 두 controller의 RBAC 마커를 동시에 제거할 수 있다.

선결 조건:
- replica set 초기화 단계에서도 mongo-go-driver가 동작해야 함
  (driver는 `directConnection=true`로 첫 멤버 Pod에 직접 연결 가능)
- localhost exception 의존을 제거하기 위해 첫 admin user 생성을 keyfile
  authentication 활성화 전 단계로 분리해야 할 수 있음

이 작업은 별도 phase로 추적되어야 하며, 본 문서는 그 phase가 완료되는
시점까지 유지된다.
