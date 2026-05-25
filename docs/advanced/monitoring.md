# Monitoring and Observability

## Overview

MongoDB Operator provides comprehensive monitoring capabilities through Prometheus integration. This guide covers setting up monitoring, dashboards, and alerting.

## Prometheus Operator Setup

### 1. Install Prometheus Operator

```bash
# Install kube-prometheus-stack (includes Prometheus Operator)
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm install prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring \
  --create-namespace \
  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false
```

### 2. Verify Installation

```bash
# Check Prometheus Operator is running
kubectl get pods -n monitoring -l app.kubernetes.io/name=kube-prometheus-stack

# Check Prometheus service
kubectl get svc prometheus-kube-prometheus-prometheus -n monitoring
```

## Enabling Monitoring in MongoDB

### Enable Monitoring

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDB
metadata:
  name: my-mongodb
  namespace: database
spec:
  members: 3
  version:
    version: "8.3.1"
  storage:
    storageClassName: standard
    size: 10Gi
  auth:
    mechanism: SCRAM-SHA-256
    adminCredentialsSecretRef:
      name: mongodb-admin
  monitoring:
    enabled: true
    serviceMonitor:
      interval: 30s
      scrapeTimeout: 10s
    prometheusRule:
      enabled: true
```

### Enable Monitoring for Sharded Cluster

```yaml
apiVersion: mongodb.keiailab.com/v1alpha1
kind: MongoDBSharded
metadata:
  name: my-sharded
  namespace: database
spec:
  version:
    version: "8.3.1"
  configServer:
    members: 3
  shards:
    count: 3
    membersPerShard: 3
  mongos:
    replicas: 2
  monitoring:
    enabled: true
```

## ServiceMonitor Configuration

> **⚠️ Important (ADR-0018)**: `spec.monitoring.serviceMonitor` field 는 *현재
> controller 미구현* — 사용자가 설정해도 ServiceMonitor 자동 생성 안 됨
> (silent ignore, deprecated). 자세한 단계적 해소 plan 은 [ADR-0018](../kb/adr/0018-monitoringspec-orphan-resolution.md)
> 참조. operator pod 자체의 metrics endpoint 는 `templates/servicemonitor.yaml`
> 정적 manifest 가 cover.

CR 인스턴스 metrics 의 ServiceMonitor 는 **사용자가 직접 작성** 필요 (아래
패턴 (controller-level 동적 생성) 도입 결정 가능.

### 운영 pod metrics 자동 ServiceMonitor (chart-level)

operator 자체 metrics scrape:

```bash
# Operator pod metrics — chart 의 templates/servicemonitor.yaml 자동 등록
kubectl get servicemonitor -n <release-namespace> -l app.kubernetes.io/name=mongodb-operator
```

### Custom ServiceMonitor (CR 인스턴스 metrics)

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: mongodb-custom
  namespace: database
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: mongodb
  endpoints:
    - port: metrics
      interval: 15s
      scrapeTimeout: 10s
      path: /metrics
```

## Grafana Dashboard Setup

### 1. Access Grafana

```bash
# Get Grafana credentials
kubectl get secret prometheus-grafana -n monitoring -o jsonpath='{.data.admin-password}' | base64 --decode

# Port forward to access Grafana
kubectl port-forward svc/prometheus-grafana 3000:80 -n monitoring
```

### 2. Import MongoDB Dashboard

**Option 1: Import from JSON**

Create `grafana-dashboard.yaml`:

```yaml
apiVersion: grafana.integreatly.org/v1beta1
kind: GrafanaDashboard
metadata:
  name: mongodb-dashboard
  namespace: monitoring
spec:
  instanceSelector:
    matchLabels:
      dashboards: "grafana"
  json: |
    {
      "title": "MongoDB Operator Metrics",
      "uid": "mongodb-operator",
      "panels": [
        {
          "title": "Connections",
          "targets": [
            {
              "expr": "mongodb_connections{job=\"my-mongodb\"}"
            }
          ]
        },
        {
          "title": "Operations per Second",
          "targets": [
            {
              "expr": "rate(mongodb_opcounters{job=\"my-mongodb\"}[5m])"
            }
          ]
        },
        {
          "title": "Replication Lag",
          "targets": [
            {
              "expr": "mongodb_replset_member_health{job=\"my-mongodb\"}"
            }
          ]
        }
      ]
    }
```

**Option 2: Manual Import**

1. Open Grafana at `http://localhost:3000`
2. Navigate to Dashboards → Import
3. Paste dashboard JSON or use dashboard ID `15789` (MongoDB Community)

## Alerting Rules Examples

### Custom PrometheusRule

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: mongodb-alerts
  namespace: database
spec:
  groups:
    - name: mongodb.rules
      rules:
        - alert: MongoDBDown
          expr: mongodb_up{job="my-mongodb"} == 0
          for: 5m
          labels:
            severity: critical
          annotations:
            summary: "MongoDB instance down"
            description: "MongoDB instance {{ $labels.instance }} is down for more than 5 minutes"

        - alert: MongoDBHighConnections
          expr: mongodb_connections{job="my-mongodb"} / mongodb_connections_limit{job="my-mongodb"} > 0.8
          for: 10m
          labels:
            severity: warning
          annotations:
            summary: "MongoDB high connections"
            description: "MongoDB {{ $labels.instance }} has {{ $value }}% connections used"

        - alert: MongoDBReplicationLag
          expr: mongodb_replset_lag_seconds{job="my-mongodb"} > 60
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "MongoDB replication lag"
            description: "Replica {{ $labels.member }} has {{ $value }}s lag"

        - alert: MongoDBMemoryHigh
          expr: mongodb_memory{job="my-mongodb"} / mongodb_memory_limit{job="my-mongodb"} > 0.9
          for: 10m
          labels:
            severity: critical
          annotations:
            summary: "MongoDB high memory usage"
            description: "MongoDB {{ $labels.instance }} using {{ $value }}% of memory limit"

        - alert: MongoDBSlowQueries
          expr: rate(mongodb_slowqueries{job="my-mongodb"}[5m]) > 10
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "MongoDB high slow query rate"
            description: "MongoDB {{ $labels.instance }} has {{ $value }} slow queries/sec"
```

## Key Metrics to Monitor

### Connection Metrics
- `mongodb_connections`: Current active connections
- `mongodb_connections_created`: Total connections created
- `mongodb_connections_available`: Available connections in pool

### Operation Metrics
- `mongodb_opcounters_insert`: Number of insert operations
- `mongodb_opcounters_query`: Number of query operations
- `mongodb_opcounters_update`: Number of update operations
- `mongodb_opcounters_delete`: Number of delete operations
- `mongodb_opcounters_getmore`: Number of getmore operations

### Replication Metrics
- `mongodb_replset_member_health`: Health status of replica set members (1=healthy)
- `mongodb_replset_lag_seconds`: Replication lag in seconds
- `mongodb_replset_member_state`: State of replica set members (PRIMARY, SECONDARY)

### Performance Metrics
- `mongodb_document_entities_returned`: Documents returned by queries
- `mongodb_document_entities_inserted`: Documents inserted
- `mongodb_slowqueries`: Number of slow queries
- `mongodb_latency_read_seconds`: Read operation latency
- `mongodb_latency_write_seconds`: Write operation latency

### Storage Metrics
- `mongodb_storage_used_bytes`: Disk space used
- `mongodb_storage_free_bytes`: Disk space available
- `mongodb_journaling_commits_in_memory`: Commits in memory
- `mongodb_journaling_commits_in_journal`: Commits to journal

## Accessing Metrics

### Using Prometheus UI

```bash
# Port forward Prometheus
kubectl port-forward svc/prometheus-kube-prometheus-prometheus 9090:9090 -n monitoring
```

Open `http://localhost:9090` and run queries:
- `mongodb_connections{job="my-mongodb"}`
- `rate(mongodb_opcounters{job="my-mongodb"}[5m])`
- `mongodb_replset_member_health{job="my-mongodb"}`

### Using kubectl

```bash
# Query metrics directly from MongoDB pod
kubectl exec -it my-mongodb-0 -n database -c mongod -- mongosh --eval 'db.serverStatus().connections'

# Get metrics endpoint
kubectl exec -it my-mongodb-0 -n database -c mongod -- curl http://localhost:9216/metrics
```

## Troubleshooting Monitoring

### Metrics Not Appearing

```bash
# Check ServiceMonitor exists
kubectl get servicemonitor -n database

# Verify metrics endpoint is reachable
kubectl exec -it my-mongodb-0 -n database -c mongod -- curl http://localhost:9216/metrics | head

# Check Prometheus is scraping
kubectl get prometheus -n monitoring
kubectl describe prometheus prometheus -n monitoring | grep -A 10 ServiceMonitors
```

### Dashboard Shows No Data

1. Verify time range is correct
2. Check data source connection
3. Verify Prometheus is scraping the job:
   ```
   up{job="my-mongodb"}
   ```
4. Check ServiceMonitor labels match Prometheus selector

### Alert Rules Not Firing

```bash
# Check Alertmanager configuration
kubectl get prometheus -n monitoring
kubectl describe prometheus prometheus -n monitoring | grep -A 20 Alertmanagers

# Verify rules are loaded
kubectl port-forward svc/prometheus-kube-prometheus-prometheus 9090:9090 -n monitoring
# Open http://localhost:9090/rules
```
