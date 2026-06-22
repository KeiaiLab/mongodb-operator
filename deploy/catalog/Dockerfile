# File-Based Catalog image for mongodb-operator (ADR-0028).
# OLM CatalogSource (image: ghcr.io/keiailab/mongodb-operator-catalog:v1.13.1) 가 본 image 를 pull.
#
# base: quay.io/operator-framework/opm — gRPC catalog server `opm serve /configs` 가 OLM 의
# resolver 에 stream. file-based catalog 는 plain YAML, opm 도구 없이 git 에서 직접 검토 가능.
FROM quay.io/operator-framework/opm:v1.42.0

# OLM 1.27+ FBC 표준 — /configs 디렉토리 의 YAML/JSON 을 자동 인식.
LABEL operators.operatorframework.io.index.configs.v1=/configs

ADD catalog /configs

# Pre-cache catalog (선택, OLM 의 resolver 응답 시간 ↓).
RUN ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache", "--cache-only"]

ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/configs", "--cache-dir=/tmp/cache"]
