# ADR-0038: OLM v1 (Path 1) 재정렬 + 7-CRD parity + drift 방지 가드

- Date: 2026-06-22
- Status: Accepted
- Authors: @phil

## Context

3개 배포 경로(ADR-0028~0030) 중 **Path 1 (OLM v1)** 이 릴리스 트레인에서 이탈한 상태로
방치돼 있었다. 감사 결과 다음 불일치가 동시에 존재했다:

- **버전 스캐터**: 라이브 operator(Helm) = `appVersion 1.13.1` 인데 Path 1 아티팩트는
  bundle CSV `1.9.0` / catalog FBC `1.5.0` / ClusterCatalog 이미지 `v1.9.0` /
  ClusterExtension `1.9.0` 로 제각각.
- **FBC 재현 불가**: 커밋된 `deploy/catalog/.../catalog.yaml` 은 `v1.5.0` 만 선언하는데
  ClusterCatalog/ClusterExtension 은 `1.9.0` 을 요구 → git 이 배포 아티팩트를 재현하지 못함.
- **3 vs 7 CRD 격차**: operator 는 7개 CRD(MongoDB / Sharded / Backup / BackupVerification /
  ClusterGroup / Federation / Insights)를 모두 reconcile 하는데(컨트롤러 등록 확인), 번들은
  **3종만** 실었다. 근본 원인은 신규 4종이 **4개 동기화 지점**(`config/crd/kustomization.yaml`,
  Makefile `sync-crds`, base CSV `owned`, `config/samples/bundle/`)에 모두 누락된 것.
- **자동화 부재**: bundle/catalog 는 수동 `make` 타깃뿐이라 매 릴리스마다 조용히 stale.
  ADR-0029 §후속 "OLM v1 release-yml 자동화" TODO 미구현, CHANGELOG `#168` 의 "CSV 4버전
  stale" 와 동일한 재발 패턴.

라이브 클러스터(2026-06-22, context `keiailab`): 활성 operator = **Helm**(`data` ns,
`:v1.13.1`, 18d). OLM `keiailab-operators` ClusterCatalog 는 SERVING 이나 mongodb-operator
**ClusterExtension 은 미설치**. CRD 7종은 Helm 이 설치 완료.

## Decision

**Path 1 아티팩트를 `appVersion`(= operator 이미지 태그 = `1.13.1`) 으로 재현 가능하게
정렬**하고, 재발을 CI 게이트로 차단한다.

1. **버전 SSOT** — OLM bundle CSV `spec.version` 은 항상 `Chart.yaml` `appVersion` 을 따른다
   (ADR-0028 결격1 관례 계승). `Chart.yaml version`(패키징 버전)은 무관. 모든 Path 1 매니페스트
   (catalog FBC, ClusterCatalog 이미지, 양 ClusterExtension, README, `docs/install.md`)를
   `1.13.1` 로 정렬.

2. **7-CRD parity** — 신규 4종을 4개 동기화 지점 전부에 반영:
   - `config/crd/kustomization.yaml` 에 4 resource 추가.
   - Makefile `sync-crds` 를 **명시 목록 → wildcard**(`config/crd/bases/mongodb.keiailab.com_*.yaml`)
     로 전환하여 향후 CRD 추가에도 자동 정합(차트 CRD drift 영구 차단). 실제로 stale 했던
     chart `mongodbfederations` CRD 가 본 작업에서 교정됨.
   - base CSV `owned` 에 7 CRD × 2 served version(v1alpha1 + v1beta1) = 14 항목 기술
     (operator-sdk 가 served 버전마다 owned 를 생성 → empty-description lint 회피).
   - `config/samples/bundle/` 에 신규 4 kind 최소 CR 샘플 추가(alm-examples).

3. **FBC 재생성은 opm render** — `catalog.yaml` 은 임베디드 bundle object 로 대용량이므로
   손편집하지 않고 `opm render <bundle-image>` 산출물로 재생성(채널/패키지 헤더만 수기).
   skipRange `>=0.3.0 <1.13.1`, `replaces: mongodb-operator.v1.9.0`(skipRange 가 실질
   upgrade edge).

4. **drift 방지 가드** — `make verify-bundle-parity`(bundle CSV version == appVersion +
   bundle CRD 개수 == `config/crd/bases`)를 신설하고 `release.yml` preflight + lefthook
   pre-push 에 연결. appVersion bump 시 번들 미재생성을 **hard-fail** 로 검출. ADR-0029
   §후속 "OLM v1 release-yml 자동화" TODO 를 (full image 빌드 자동화가 아닌) parity 게이트
   형태로 부분 해소.

5. **클러스터 적용 범위** — 갱신된 ClusterCatalog(`mongodb-operator-catalog:v1.13.1`)만
   apply 하여 catalog 가 1.13.1 을 서빙하게 한다. **ClusterExtension 설치는 보류** — 라이브
   Helm operator 와 동시 운영 시 cluster-scoped CRD 이중 reconcile 충돌이 발생하므로, Helm→OLM
   *컷오버*(Helm 릴리스 선 제거)는 비가역 운영 작업으로 별도 결정/승인 대상이다(Autonomy
   Constitution).

6. **번들 CRD `maxDescLen=0` 축소 (kind e2e 후속, 2026-06-22)** — OLM v1(operator-controller)
   은 번들 전체를 helm release Secret(etcd **1MiB 한도**)에 저장한다. full-description CRD
   (mongodbshardeds 3.2MB 등, 합계 4.56MB)는 gzip 후에도 한도를 초과해 ClusterExtension 설치가
   `Secret … Too long: may not be more than 1048576 bytes` 로 실패한다(kind e2e 실증). `make bundle`
   에 `controller-gen crd:maxDescLen=0` 단계를 추가해 ***번들 CRD 만*** description 을 제거(합계
   ~1.5MB) — `config/crd/bases`·`charts/` 는 full description 을 유지해 Helm/`kubectl explain` UX
   를 보존한다. `verify-bundle-parity` 에 번들 CRD 합계 `< 2.5MiB` 가드 추가. 부수효과: OLM 설치
   클러스터의 `kubectl explain` 은 field description 미표시(Go types 주석·docs 로 대체).

## Consequences

**긍정적**:
- Path 1 이 Helm(Path 2)과 동일한 7-CRD / 1.13.1 표면으로 재현 가능 — 두 경로 사용자가
  동일 API 를 받는다.
- `verify-bundle-parity` 로 "appVersion bump 후 번들 stale" 재발이 release/pre-push 에서 차단.
- `sync-crds` wildcard 화로 신규 CRD 추가 시 chart/bundle drift 자동 방지.
- catalog FBC ↔ ClusterCatalog ↔ ClusterExtension 버전이 git 에서 일치 — 재현성 회복.

**부정적 / 트레이드오프**:
- appVersion bump 시 `make bundle VERSION=<appVersion>` 재생성이 **의무**가 된다(가드가
  강제). 의도된 마찰 — Path 1 stale 의 근본 차단 비용.
- opm render 가 7 CRD(대용량 `mongodbshardeds` 포함)를 포함하므로 `catalog.yaml` 크기 증가.
  `--migrate-level=bundle-object-to-csv-metadata` 로 경량화 가능(후속 검토).
- OLM v1 은 catalog 만 라이브이고 ClusterExtension 은 미설치 — Path 1 "라이브 operator"
  상태는 컷오버 전까지 보류.
- `make deploy`(L393 `kustomize edit set image controller=`)는 manager.yaml 의 실제 이미지명
  (`ghcr.io/keiailab/mongodb-operator`)과 불일치하는 placeholder 를 갱신하는 **pre-existing
  버그**(동일 부류의 image-name drift). 본 ADR 범위(OLM Path 1) 밖이라 별도 후속으로 남김.

## Alternatives Considered

- **catalog.yaml 버전 문자열만 손편집**: 배제. FBC 가 bundle object 를 임베드하므로 버전만
  바꾸면 임베디드 객체(3 CRD, 1.5.0)가 stale 로 남아 재현 불가. opm render 가 정석.
- **번들을 core-3 CRD 로 유지**: 배제. operator 가 7 CRD 를 모두 reconcile 하므로 3종 번들은
  Path 1 사용자에게 API 4종 누락 = 결함. "일관성" 의 정의에 반함.
- **Path 1 동결 / OLM 폐기**: 배제. ADR-0029 가 OLM v1 을 "recommended, current modern
  standard" 로 sealed. 동결은 결정 위반.
- **ClusterExtension 즉시 라이브 설치**: 배제. 라이브 Helm operator 와 이중 reconcile 충돌.
  컷오버는 Helm 제거와 원자적으로 수행해야 함.

## Refs

- ADR-0023: OperatorHub.io bundle scaffold (operator-sdk, owned/alm-examples 패턴)
- ADR-0028: OLM 번들 외부 사용자 운영 수준 (skipRange/replaces/containerImage 관례)
- ADR-0029: OLM v1 채택 (§후속 "OLM v1 release-yml 자동화" TODO 부분 해소)
- ADR-0030: OLM v1 narrow installer RBAC (본 ADR 에서 7-CRD 로 RBAC 정정)
- ADR-0037: GitOps overlay + ArtifactHub 표준화 (GH Actions OSS 정당화 계승)
