# ──────────────────────────────────────────────────────────────────────────
# oplog-tailer.Dockerfile — PITR oplog 증분 스트리밍 사이드카 이미지.
#
# 왜 별도 이미지인가: 사이드카 스트림 스크립트(internal/assets/scripts/
# oplog-stream.sh.tpl)는
#     mongodump --db=local -c oplog.rs --query ... --out=- | gzip -c | aws s3 cp -
# 로 mongodump(+mongosh)와 aws CLI 를 *한 컨테이너*에서 함께 쓴다. 공식 mongo
# 이미지엔 aws CLI 가 없고, 사이드카는 mongod 와 같은 pod 의 non-root(999)라
# 런타임 apt-get 설치도 불가하다 (backup Job 은 root 라 가능). 두 도구를 미리
# 담은 이미지를 빌드해 operator Deployment 의 OPLOG_TAILER_IMAGE 로 주입한다
# (internal/resources/oplog_tailer.go resolveOplogTailerImage 참조).
#
# 빌드: make oplog-tailer-image-build (로컬) / oplog-tailer-image-push (Harbor).
#
# GOVERNANCE §2.3: push 대상은 linux/amd64 단일 아키텍처만 (멀티아키 manifest
# list 금지). 아래 TARGETARCH 분기는 로컬 *단일-arch* 빌드(Apple Silicon 등)
# 편의용이며 멀티아키 이미지를 만들지 않는다 — push 는 PLATFORMS=linux/amd64.
# ──────────────────────────────────────────────────────────────────────────

# mongo 8.0.x = 현행 GA/LTS 트레인 (8.1/8.2/8.3 = rapid release — 데이터
# 내구성 컴포넌트엔 부적합). mongodump 는 major 내 server 버전과 호환되므로
# 단일 pin 으로 충분하다. 배포 mongod 와 정확히 맞추려면 빌드 시
# --build-arg MONGO_VERSION=<x.y.z> 로 override.
ARG MONGO_VERSION=8.0.4
FROM mongo:${MONGO_VERSION}

# buildx 가 --platform 에서 자동 주입 (operator Dockerfile 과 동일 관례).
ARG TARGETARCH

# aws CLI v2 — restore fetch init-container(amazon/aws-cli:2.36.1,
# builder_backup.go awsCLIImage)와 동일 버전 관례. mongo 이미지는 debian
# bookworm 계열이라 공식 v2 zip 으로 설치한다 (debian 저장소 awscli 는 v1 —
# EOL). :latest 금지(불변성) — CVE 패치 시 본 ARG 만 갱신.
ARG AWSCLI_VERSION=2.36.1
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) awsarch=x86_64 ;; \
      arm64) awsarch=aarch64 ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH:-unset}" >&2; exit 1 ;; \
    esac; \
    apt-get update; \
    apt-get install -y --no-install-recommends curl unzip ca-certificates; \
    curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${awsarch}-${AWSCLI_VERSION}.zip" -o /tmp/awscliv2.zip; \
    unzip -q /tmp/awscliv2.zip -d /tmp; \
    /tmp/aws/install --bin-dir /usr/local/bin --install-dir /usr/local/aws-cli; \
    rm -rf /tmp/awscliv2.zip /tmp/aws /var/lib/apt/lists/*; \
    # 빌드 시점 계약 검증 — 둘 중 하나라도 없으면 빌드가 여기서 실패한다.
    aws --version; \
    mongodump --version

# 런타임: 사이드카는 pod SecurityContext 로 non-root(999) 강제 구동되고
# (BuildOplogTailerSidecar), K8s 가 Command(/bin/bash -c script)로 image
# ENTRYPOINT 를 덮으므로 mongo 의 docker-entrypoint.sh 는 타지 않는다.
# USER/ENTRYPOINT 를 재정의하지 않는 이유 — 어차피 K8s 가 덮는다 (표면 최소화).
