#!/usr/bin/env bash
# resume-after-unflag.sh — Re-run blocked awesome-list submissions once the
# GitHub account flag (INC-0001) clears.
#
# A flagged account returns core rate-limit 60 / graphql 0 and blocks fork
# creation. This script verifies the flag has cleared, then forks + opens PRs
# to the active awesome lists.
#
# Usage: ./scripts/resume-after-unflag.sh

set -euo pipefail

REPO_URL="https://github.com/keiailab/mongodb-operator"

echo "==> Checking GitHub account flag status ..."
PROFILE_CODE=$(curl -sS -o /dev/null -w "%{http_code}" https://github.com/eightynine01)
CORE_LIMIT=$(curl -sS -H "Authorization: Bearer $(gh auth token)" https://api.github.com/rate_limit \
  | python3 -c "import json,sys; print(json.load(sys.stdin)['resources']['core']['limit'])")

echo "    profile (unauthed): HTTP $PROFILE_CODE"
echo "    core rate limit: $CORE_LIMIT"

if [ "$PROFILE_CODE" != "200" ] || [ "$CORE_LIMIT" -lt 1000 ]; then
  echo "ERROR: account still flagged (profile=$PROFILE_CODE, core=$CORE_LIMIT)." >&2
  echo "  Appeal first: https://support.github.com/contact?tags=account-flagged" >&2
  echo "  See docs/kb/incident/INC-0001-github-account-flagged.md" >&2
  exit 1
fi

echo "==> Flag cleared. Submitting awesome-list PRs ..."

submit() {
  local upstream="$1" entry="$2" section_hint="$3"
  local name="${upstream##*/}"
  echo "--- $upstream ---"
  gh repo fork "$upstream" --clone --default-branch-only 2>&1 | tail -1
  cd "$name"
  # Insert entry after the first line matching the section hint.
  if grep -q "$section_hint" README.md; then
    awk -v e="$entry" -v h="$section_hint" '
      {print}
      $0 ~ h && !done {print ""; print e; done=1}
    ' README.md > README.md.new && mv README.md.new README.md
  else
    echo "  WARN: section '$section_hint' not found in $name/README.md — add manually" >&2
  fi
  git checkout -b add-mongodb-operator
  git add README.md
  git commit -s -m "Add mongodb-operator (Kubernetes operator for MongoDB)"
  git push -u origin add-mongodb-operator
  gh pr create --repo "$upstream" --title "Add mongodb-operator" \
    --body "Kubernetes operator for MongoDB ReplicaSets + Sharded Clusters + Backup. Apache-2.0. $REPO_URL"
  cd ..
  rm -rf "$name"
}

submit "ramnes/awesome-mongodb" \
  "* [mongodb-operator]($REPO_URL) - Kubernetes Operator managing MongoDB ReplicaSets, Sharded Clusters, and automated backups via CRDs. Apache-2.0." \
  "Deployment"

submit "ramitsurana/awesome-kubernetes" \
  "- [mongodb-operator]($REPO_URL) - Kubernetes Operator for MongoDB ReplicaSets and Sharded Clusters with built-in backup, TLS, LDAP, and monitoring. Apache-2.0." \
  "Operators"

echo "==> Done. Verify with: gh pr list --repo ramnes/awesome-mongodb --author eightynine01"
