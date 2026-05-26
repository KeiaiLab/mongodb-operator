# OperatorHub.io Submission Guide

## Prerequisites (all met)
- [x] OLM bundle in `bundle/manifests/`
- [x] ClusterServiceVersion with `alm-examples`
- [x] 3 CRDs (MongoDB, MongoDBSharded, MongoDBBackup)
- [x] Apache-2.0 license
- [x] Container image on GHCR

## Submission Steps

1. Fork https://github.com/k8s-operatorhub/community-operators
2. Create directory: `operators/mongodb-operator/1.9.0/`
3. Copy bundle contents:
   ```bash
   cp bundle/manifests/mongodb-operator.clusterserviceversion.yaml \
      operators/mongodb-operator/1.9.0/
   cp config/crd/bases/*.yaml \
      operators/mongodb-operator/1.9.0/
   cp bundle/metadata/annotations.yaml \
      operators/mongodb-operator/1.9.0/metadata/
   ```
4. Submit PR to community-operators repo
5. Wait for CI validation + review

## Bundle Verification

```bash
operator-sdk bundle validate ./bundle --select-optional suite=operatorframework
```
