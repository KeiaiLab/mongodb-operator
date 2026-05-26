# Awesome List Submissions — Runbook

Status as of v1.9.0 community launch:

| List | Status | Notes |
|---|---|---|
| operator-framework/awesome-operators | ❌ Skip | Archived since 2021-08, no longer accepts PRs |
| ramnes/awesome-mongodb | ⏳ Pending | Active (2642★). Blocked by transient GitHub fork throttle |
| ramitsurana/awesome-kubernetes | ⏳ Pending | Active (15.9k★). Blocked by transient GitHub fork throttle |
| avelino/awesome-go | ⏳ Optional | Strict CI; lower relevance |

> **GitHub fork throttle**: Creating multiple forks in a short window returns
> `403 You cannot fork this repository at this time`. This is separate from the
> API rate limit and clears within ~1 hour. Retry the commands below after the
> throttle resets.

## Entry Texts

### ramnes/awesome-mongodb — "Deployment" or "Administration" section

```markdown
* [mongodb-operator](https://github.com/keiailab/mongodb-operator) - Kubernetes Operator managing MongoDB ReplicaSets, Sharded Clusters, and automated backups via CRDs. Apache-2.0.
```

### ramitsurana/awesome-kubernetes — "Database > Operators" section

```markdown
- [mongodb-operator](https://github.com/keiailab/mongodb-operator) - Kubernetes Operator for MongoDB ReplicaSets and Sharded Clusters with built-in backup, TLS, LDAP, and monitoring. Apache-2.0.
```

### avelino/awesome-go — "Database > Database Tools" (optional)

```markdown
- [mongodb-operator](https://github.com/keiailab/mongodb-operator) - Kubernetes Operator for MongoDB with declarative ReplicaSet, Sharded Cluster, and Backup management.
```

## Runnable Commands (after fork throttle clears)

```bash
# ramnes/awesome-mongodb
gh repo fork ramnes/awesome-mongodb --clone
cd awesome-mongodb
# Edit README.md: add entry in the appropriate section
git checkout -b add-mongodb-operator
git add README.md && git commit -s -m "Add mongodb-operator (Kubernetes operator)"
git push -u origin add-mongodb-operator
gh pr create --repo ramnes/awesome-mongodb --title "Add mongodb-operator" \
  --body "Kubernetes operator for MongoDB ReplicaSets + Sharded Clusters + Backup. Apache-2.0, production-proven."
cd .. && rm -rf awesome-mongodb

# ramitsurana/awesome-kubernetes
gh repo fork ramitsurana/awesome-kubernetes --clone
cd awesome-kubernetes
# Edit README.md: Database > Operators section
git checkout -b add-mongodb-operator
git add README.md && git commit -s -m "Add mongodb-operator under Database/Operators"
git push -u origin add-mongodb-operator
gh pr create --repo ramitsurana/awesome-kubernetes --title "Add mongodb-operator" \
  --body "Kubernetes operator for MongoDB. Apache-2.0."
cd .. && rm -rf awesome-kubernetes
```

## Completed

- **OperatorHub.io (k8s-operatorhub/community-operators)**: PR #8207 submitted
  as package `keiailab-mongodb-operator` v1.9.0 (the `mongodb-operator` package
  name is owned by opstreelabs, so a unique name was used).
