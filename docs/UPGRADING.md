# Upgrading mongodb-operator

본 문서는 mongodb-operator의 마이너 버전 업그레이드 시 필요한 마이그레이션
작업을 정리한다. Helm 사용자는 chart 업그레이드만으로 모든 변경이 적용되지만,
정적 manifest(`kubectl apply -f`) 사용자는 RBAC 등 일부 항목을 수동으로
patch해야 한다.

## 1.0.x → 1.1.x

### Helm 사용자

```bash
helm repo update
helm upgrade mongodb-operator <repo>/mongodb-operator \
  --namespace mongodb-operator-system \
  --version 1.1.x
```

차트 자체가 RBAC·CRD·Deployment를 모두 동기화한다. 추가 작업 불필요.

### 정적 manifest 사용자 — RBAC 마이그레이션 필수

1.1.0은 controller에 다음 신규 권한 3종을 추가했다.

| API group | Resource | 사유 |
|---|---|---|
| `coordination.k8s.io` | `leases` | 부트스트랩 race-free를 위한 분산 락(ADR-0002) |
| `networking.k8s.io` | `networkpolicies` | RS·Sharded 네트워크 격리 자동화(P0-4) |
| `policy` | `poddisruptionbudgets` | 멤버 단위 maintenance 보호(P0-4) |

기존 ClusterRole에 다음을 추가한다(ClusterRole 이름이 `manager-role`인 경우
의 예시 — 환경에 맞게 변경).

```bash
kubectl patch clusterrole manager-role --type=json -p='[
  {"op":"add","path":"/rules/-","value":{
    "apiGroups":["coordination.k8s.io"],
    "resources":["leases"],
    "verbs":["create","delete","get","list","patch","update","watch"]
  }},
  {"op":"add","path":"/rules/-","value":{
    "apiGroups":["networking.k8s.io"],
    "resources":["networkpolicies"],
    "verbs":["create","delete","get","list","patch","update","watch"]
  }},
  {"op":"add","path":"/rules/-","value":{
    "apiGroups":["policy"],
    "resources":["poddisruptionbudgets"],
    "verbs":["create","delete","get","list","patch","update","watch"]
  }}
]'
```

> **권한 누락 시 증상** — controller pod 로그에 다음이 반복된다.
> ```
> Failed to watch *v1.NetworkPolicy: networkpolicies.networking.k8s.io is forbidden:
> User "system:serviceaccount:...:..." cannot list resource "networkpolicies" at the cluster scope
> ```
> cache sync 실패로 `Reconcile`이 호출되지 않으며, 기존 CR은 갱신되지 않는다.

### 1.1.0 자체의 알려진 결함 → 1.1.1로 업그레이드 권고

1.1.0의 `IsInitialized`는 익명 매니저로 `replSetGetStatus`를 호출한 결과가
`Unauthorized(13)`이면 generic error로 propagate한다. 부트스트랩이 RS init
까지만 완료된 채 admin user 생성 단계에서 중단된 CR은 다음 reconcile에서
영원히 admin user 부트스트랩 분기로 진입하지 못해 `Phase=Failed`로 stuck
된다(INC-0001). 1.1.1은 이 분기를 init-completed 시그널로 해석해 자동
복구하도록 수정했다(ADR-0004).

1.1.0 → 1.1.1 업그레이드는 controller 이미지 교체만으로 충분하다(스키마/RBAC
변경 없음).

```bash
kubectl -n mongodb-operator-system set image \
  deploy/mongodb-operator-controller-manager \
  manager=ghcr.io/<repo>/mongodb-operator:v1.1.1
```

## 0.x → 1.0.x

API group `mongodb.keiailab.com/v1alpha1` 신규 도입. 기존 0.x 리소스는 변환
없음 — 1.0.x는 첫 stable. 새 클러스터에 설치 권장.
