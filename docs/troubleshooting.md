# Troubleshooting

## Overview

This guide helps diagnose and resolve common issues when using MongoDB Operator. Use the debugging tips and resolution steps to quickly identify and fix problems.

## Common Deployment Issues

### Operator Not Starting

**Symptoms:**
- Operator pods stuck in `Pending` or `CrashLoopBackOff` state
- CRDs not reconciling

**Diagnosis:**
```bash
# Check operator pod status
kubectl get pods -n mongodb-operator-system

# Check operator logs
kubectl logs -n mongodb-operator-system -l app.kubernetes.io/name=mongodb-operator --tail=50

# Check for resource constraints
kubectl describe pod -n mongodb-operator-system -l app.kubernetes.io/name=mongodb-operator
```

**Resolution:**
1. Verify CRDs are installed:
   ```bash
   kubectl get crd | grep mongodb
   ```
2. Check RBAC permissions:
   ```bash
   kubectl get clusterrole,clusterrolebinding | grep mongodb
   ```
3. Ensure sufficient resources:
   ```yaml
   # Add resource limits to operator deployment
   resources:
     requests:
       memory: "256Mi"
       cpu: "100m"
     limits:
       memory: "512Mi"
       cpu: "500m"
   ```

### MongoDB Pods Not Starting

**Symptoms:**
- Pods stuck in `Init` or `ContainerCreating` phase
- Repeated crash loops

**Diagnosis:**
```bash
# Check pod status
kubectl get pods -n database -l app.kubernetes.io/name=mongodb

# Describe pod to see events
kubectl describe pod my-mongodb-0 -n database

# Check pod logs
kubectl logs my-mongodb-0 -n database -c mongod
kubectl logs my-mongodb-0 -n database --all-containers=true
```

**Resolution:**

**PVC Issues:**
```bash
# Check PVC status
kubectl get pvc -n database

# If PVC is pending, check storage class
kubectl get storageclass

# Delete stuck PVC and recreate
kubectl delete pvc data-my-mongodb-0 -n database
```

**Image Pull Issues:**
```bash
# Check image pull secrets
kubectl get secrets -n database

# Verify image is accessible
docker pull mongo:8.3.1

# Check if registry requires authentication
kubectl describe pod my-mongodb-0 -n database | grep Image
```

### ReplicaSet Initialization Failed

**Symptoms:**
- Pods running but cluster not ready
- Status shows initialization errors

**Diagnosis:**
```bash
# Check MongoDB status
kubectl get mongodb my-mongodb -n database -o yaml

# Connect to primary candidate
kubectl exec -it my-mongodb-0 -n database -c mongod -- mongosh

# Check replica set status
rs.status()
rs.conf()
```

**Resolution:**
1. Verify keyfile secret exists:
   ```bash
   kubectl get secret my-mongodb-keyfile -n database
   ```

2. Check network policies allow pod communication:
   ```bash
   kubectl get networkpolicies -n database
   ```

3. Manually initiate if automated process failed:
   ```bash
   kubectl exec -it my-mongodb-0 -n database -c mongod -- \
     mongosh --eval 'rs.initiate({_id: "my-mongodb", members: [{_id: 0, host: "my-mongodb-0.my-mongodb:27017"}, {_id: 1, host: "my-mongodb-1.my-mongodb:27017"}, {_id: 2, host: "my-mongodb-2.my-mongodb:27017"}]})'
   ```

## Connection Problems

### Cannot Connect to MongoDB

**Symptoms:**
- Connection timeout errors
- Authentication failures

**Diagnosis:**
```bash
# Test connectivity from another pod
kubectl run debug --rm -it --image=mongo:8.3.1 -- sh
# Inside pod
mongosh mongodb://admin:password@my-mongodb.database.svc.cluster.local:27017/admin

# Check service
kubectl get svc -n database
kubectl describe svc my-mongodb -n database
```

**Resolution:**

**Wrong Connection String:**
- Verify service name and namespace
- Check credentials secret
- Ensure TLS is configured correctly if enabled

**Service Issues:**
```bash
# Check if service has endpoints
kubectl get endpoints my-mongodb -n database

# Verify pods are running
kubectl get pods -n database -l app.kubernetes.io/name=mongodb

# Check if using correct service type
kubectl describe svc my-mongodb -n database | grep Type
```

**Authentication Issues:**
```bash
# Verify admin credentials
kubectl get secret mongodb-admin -n database -o yaml

# Test authentication
kubectl exec -it my-mongodb-0 -n database -c mongod -- \
  mongosh -u admin -p $(kubectl get secret mongodb-admin -n database -o jsonpath='{.data.password}' | base64 --decode) --eval 'db.adminCommand("ping")'
```

### Connection from Application Fails

**Diagnosis:**
```bash
# Check network policies
kubectl get networkpolicies -n database

# Check if application is in same namespace
kubectl get pods --all-namespaces -l app=your-app

# Test from application pod
kubectl exec -it your-app-pod -- nslookup my-mongodb.database.svc.cluster.local
kubectl exec -it your-app-pod -- telnet my-mongodb.database.svc.cluster.local 27017
```

**Resolution:**

**Network Policies:**
```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-mongodb
  namespace: database
spec:
  podSelector: {}
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: app-namespace
      ports:
        - protocol: TCP
          port: 27017
```

**DNS Issues:**
```bash
# Check CoreDNS pods
kubectl get pods -n kube-system -l k8s-app=kube-dns

# Test DNS resolution
kubectl run dns-test --rm -it --image=busybox -- nslookup my-mongodb.database.svc.cluster.local
```

## StatefulSet Pod Issues

### Pod Stuck in Termination

**Symptoms:**
- Pod stuck in `Terminating` state
- New pods cannot be created

**Diagnosis:**
```bash
# Check pod status
kubectl get pod my-mongodb-2 -n database

# Force delete if stuck (use with caution)
kubectl delete pod my-mongodb-2 -n database --force --grace-period=0

# Check StatefulSet status
kubectl get statefulset my-mongodb -n database -o yaml
```

**Resolution:**

**Finalizer Issues:**
```bash
# Remove finalizers (last resort)
kubectl patch pod my-mongodb-2 -n database -p '{"metadata":{"finalizers":[]}}' --type=merge
```

**Pod Deletion Delay:**
- MongoDB pods have `preStop` hooks for graceful shutdown
- Increase `terminationGracePeriodSeconds` if needed:
  ```yaml
  spec:
    terminationGracePeriodSeconds: 600
  ```

### Pod Restart Loop

**Symptoms:**
- Pod repeatedly restarts
- CrashLoopBackOff status

**Diagnosis:**
```bash
# Check pod restart count
kubectl get pod my-mongodb-0 -n database

# View previous logs
kubectl logs my-mongodb-0 -n database -c mongod --previous

# Check resource usage
kubectl top pod my-mongodb-0 -n database
```

**Resolution:**

**OOM Kill:**
```bash
# Check if pod was killed due to OOM
kubectl describe pod my-mongodb-0 -n database | grep -i oom

# Increase memory limits
kubectl patch mongodb my-mongodb -n database --type='merge' -p '{
  "spec": {
    "resources": {
      "limits": {"memory": "4Gi"}
    }
  }
}'
```

**Configuration Errors:**
```bash
# Check mongod.conf in ConfigMap
kubectl get configmap my-mongodb-config -n database -o yaml

# Verify MongoDB version compatibility
kubectl exec -it my-mongodb-0 -n database -c mongod -- mongod --version
```

## Init Script Failures

### Initialization Script Errors

**Symptoms:**
- Init container fails
- Pod stuck in `Init` phase

**Diagnosis:**
```bash
# Check init container logs
kubectl logs my-mongodb-0 -n database -c init-mongodb

# View init container spec
kubectl get pod my-mongodb-0 -n database -o yaml | grep -A 20 initContainers
```

**Resolution:**

**Script Timeout:**
- Increase init container timeout in operator configuration

**Permission Issues:**
```bash
# Check if init container has proper permissions
kubectl exec -it my-mongodb-0 -n database -c mongod -- ls -la /data

# Verify security context
kubectl get pod my-mongodb-0 -n database -o yaml | grep -A 10 securityContext
```

**Keyfile Issues:**
```bash
# Verify keyfile exists
kubectl exec -it my-mongodb-0 -n database -c mongod -- cat /data/keyfile

# Check keyfile permissions (should be 0600)
kubectl exec -it my-mongodb-0 -n database -c mongod -- stat /data/keyfile
```

## Debugging Tips

### Collect Debug Information

```bash
# Create debug namespace
kubectl create namespace debug

# Gather all MongoDB resources
kubectl get all,mongodb,mongodbsharded,mongodbbackup -n database -o yaml > debug/cluster-state.yaml

# Collect pod logs
kubectl logs -n database -l app.kubernetes.io/name=mongodb --all-containers=true > debug/pods.log

# Collect events
kubectl get events -n database --sort-by='.lastTimestamp' > debug/events.log

# Describe resources
kubectl describe pod -n database -l app.kubernetes.io/name=mongodb > debug/pods-describe.txt
kubectl describe statefulset -n database > debug/statefulset-describe.txt
```

### Use kubectl Debug

```bash
# Debug running pod
kubectl debug -it my-mongodb-0 -n database --image=busybox --target=mongod

# Debug with ephemeral container
kubectl debug my-mongodb-0 -n database -it --image=nicolaka/netshoot --share-processes
```

### Enable Operator Debug Logging

```yaml
# Edit operator deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mongodb-operator
  namespace: mongodb-operator-system
spec:
  template:
    spec:
      containers:
        - name: manager
          env:
            - name: LOG_LEVEL
              value: "debug"
```

### Check Reconciliation Status

```bash
# Get MongoDB CRD status
kubectl get mongodb my-mongodb -n database -o jsonpath='{.status}'

# Check conditions
kubectl get mongodb my-mongodb -n database -o jsonpath='{.status.conditions}' | jq .

# Check for errors in status
kubectl get mongodb my-mongodb -n database -o jsonpath='{.status.conditions[?(@.type=="Ready")]}'
```

### Monitor Operator Events

```bash
# Watch operator events
kubectl get events -n mongodb-operator-system -w

# Filter events for MongoDB resources
kubectl get events -n database --field-selector involvedObject.kind=MongoDB

# Get warning events
kubectl get events -n database --field-selector type=Warning
```

### Verify Internal State

```bash
# Connect to MongoDB and check internal state
kubectl exec -it my-mongodb-0 -n database -c mongod -- mongosh

# Inside mongosh
db.adminCommand({replSetGetStatus: 1})
db.adminCommand({serverStatus: 1})
db.adminCommand({getCmdLineOpts: 1})
```

## Common Error Messages

### "No primary set in replica set"

**Cause:** ReplicaSet election in progress or no primary elected

**Solution:**
```bash
# Check replica set status
kubectl exec -it my-mongodb-0 -n database -c mongod -- mongosh --eval 'rs.status()'

# Force election if needed
kubectl exec -it my-mongodb-0 -n database -c mongod -- mongosh --eval 'rs.stepDown(60)'
```

### "Connection refused"

**Cause:** MongoDB not listening on expected port

**Solution:**
```bash
# Check port configuration
kubectl exec -it my-mongodb-0 -n database -c mongod -- netstat -tlnp | grep mongod

# Verify service port matches MongoDB port
kubectl get svc my-mongodb -n database -o yaml | grep port
```

### "Authentication failed"

**Cause:** Incorrect credentials or mechanism

**Solution:**
```bash
# Verify credentials secret
kubectl get secret mongodb-admin -n database -o jsonpath='{.data.password}' | base64 --decode

# Check authentication mechanism in CRD
kubectl get mongodb my-mongodb -n database -o yaml | grep -A 5 auth
```

### "Insufficient storage"

**Cause:** Not enough disk space

**Solution:**
```bash
# Check disk usage
kubectl exec -it my-mongodb-0 -n database -c mongod -- df -h

# Expand PVC or migrate to larger storage
kubectl patch pvc data-my-mongodb-0 -n database -p '{"spec":{"resources":{"requests":{"storage":"20Gi"}}}}'
```

## Getting Help

If issues persist:

1. Collect debug information (see "Debugging Tips" section)
2. Check [GitHub Issues](https://github.com/keiailab/mongodb-operator/issues)
3. Create a detailed issue with:
   - MongoDB Operator version
   - Kubernetes version
   - MongoDB version
   - Full error logs
   - CRD configuration
   - Steps to reproduce
