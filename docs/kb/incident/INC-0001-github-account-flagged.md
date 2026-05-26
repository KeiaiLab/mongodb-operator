# INC-0001: GitHub account flagged — external fork/PR blocked

- Detected: 2026-05-26 11:35 (KST)
- Resolved: (pending — requires GitHub Support appeal)
- Severity: SEV-3 (community-launch automation blocked; core development unaffected)
- Owners: @eightynine01
- Tags: [github, rate-limit, account, automation]

## Impact

- **User-facing**: none — the operator, Helm chart, container image, and OLM bundle are all unaffected.
- **Automation**: external `fork` + `pull request` creation from the `eightynine01` account is blocked. awesome-list submissions (awesome-mongodb, awesome-kubernetes) could not be created.
- **Not affected**: keiailab org repos (HTTP 200), `git push` (git protocol), OperatorHub.io PR #8207 (submitted before budget exhaustion).

## Timeline

- Multi-session automation (v1.6.0 → v1.9.0): 4 releases, 20+ commits, 4 Docker pushes, GitHub Release/topics/fork API calls in a compressed window.
- 11:35 — `gh api -X POST .../forks` returned `403 You cannot fork this repository at this time`.
- 11:48 — initial hypothesis "fork throttle" recorded.
- 11:52 — evidence-based RCA: measured rate limits + profile visibility.
- 11:53 — root cause confirmed: account flagged.

## Root Cause

The `eightynine01` GitHub account was **flagged by GitHub's automated abuse/spam detection**. Confirmed by the combined signature (all measured):

| Probe | Result | Meaning |
|---|---|---|
| `GET /users/eightynine01` (authed) | 200 | account exists, not deleted |
| `GET /users/eightynine01` (unauthed) | **404** | hidden from logged-out users |
| `GET github.com/eightynine01` (unauthed) | **404** | profile page hidden |
| `GET /rate_limit` core | **60** (normal 5000) | downgraded to unauthenticated tier |
| `GET /rate_limit` graphql | **0** (normal 5000) | GraphQL fully blocked |
| `POST /repos/*/forks` | 403 "cannot fork at this time" | content creation blocked |
| `GET /repos/keiailab/mongodb-operator` | 200 | org/repo unaffected (user-level flag) |
| `git push origin main` | success | git protocol unaffected |
| `/user/emails` | both verified | unverified-email cause ruled out |
| `/user` | created 2017, 2FA on, active | suspension ruled out |

**5 Whys**:
1. Why did fork fail? → 403 "cannot fork at this time" (content creation blocked).
2. Why blocked? → account API privileges reduced (core 60, graphql 0).
3. Why reduced? → account is flagged (profile 404 to logged-out users).
4. Why flagged? → GitHub abuse detection triggered.
5. Why triggered? → high-velocity automated activity (rapid releases/commits/API calls) matched bot/spam heuristics.

## Resolution

**The only fix is a manual GitHub Support appeal by the account owner** — a flagged account cannot be un-flagged via API, token rotation, or any code change.

1. Check eightynine01@gmail.com for a GitHub "account flagged" notice, or a banner on github.com/eightynine01 when logged in.
2. Submit an appeal at https://support.github.com/contact?tags=account-flagged (or https://github.com/contact). State: 8-year-old account, legitimate open-source development (mongodb-operator), automation tooling may have caused a false positive.
3. GitHub manually reviews (typically 1–3 business days).
4. Some flags auto-clear within 24h — re-measure before escalating.

**Verify resolution**:
```bash
curl -sS -o /dev/null -w "%{http_code}\n" https://github.com/eightynine01   # 200 = cleared, 404 = still flagged
curl -sS -H "Authorization: Bearer $(gh auth token)" https://api.github.com/rate_limit \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print('core', d['resources']['core']['limit'], 'graphql', d['resources']['graphql']['limit'])"
# cleared: core 5000 graphql 5000
```

## Prevention

- **Throttle automation velocity**: batch commits, avoid rapid-fire content-creation API calls. Add a `gh api rate_limit` pre-check (core < 100 → pause) before bulk fork/PR loops.
- **Separate human vs automation identity**: consider a dedicated machine account or GitHub App (App installations have higher, separate limits) for high-volume automation, keeping the personal account clean.
- **`scripts/resume-after-unflag.sh`**: re-runs the blocked awesome-list submissions once the flag clears.

## Action Items

- [ ] AI-0001: Owner appeals to GitHub Support (Owner: @eightynine01)
- [ ] AI-0002: After un-flag, run `scripts/resume-after-unflag.sh`
- [ ] AI-0003: Evaluate GitHub App for future automation (Owner: @eightynine01)
