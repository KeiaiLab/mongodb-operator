#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/mongodb-operator-artifacthub-smoke-test.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

stubbin="$tmpdir/bin"
mkdir -p "$stubbin"

cat >"$stubbin/helm" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
	"repo add"|"repo update")
		exit 0
		;;
	"search repo")
		printf 'NAME                               CHART VERSION      APP VERSION\n'
		printf 'keiailab-mongodb-operator/mongodb-operator 0.3.0-alpha.16 0.3.0-alpha.16\n'
		exit 0
		;;
	"show chart")
		printf 'version: 1.12.4\nappVersion: 1.12.2\n'
		exit 0
		;;
esac
echo "unexpected helm call: $*" >&2
exit 99
SH
chmod +x "$stubbin/helm"

cat >"$stubbin/curl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

out=""
args=()
while [[ $# -gt 0 ]]; do
	case "$1" in
		-o)
			out="$2"
			shift 2
			;;
		-f|-s|-S|-L|-fsSL)
			shift
			;;
		*)
			args+=("$1")
			shift
			;;
	esac
done

last_index=$((${#args[@]} - 1))
url="${args[$last_index]}"
if [[ -z "$out" ]]; then
	out="/dev/stdout"
fi

case "$url" in
	*/index.yaml)
		printf 'apiVersion: v1\nentries:\n  mongodb-operator: []\n' >"$out"
		;;
	*/artifacthub-repo.yml)
		printf 'repositoryID: test-id\n' >"$out"
		;;
	*/repositories/search*)
		if [[ "${ARTIFACTHUB_TEST_CASE:-missing}" == "registered" ]]; then
			printf '[{"repository_id":"repo-id","name":"keiailab-mongodb-operator","url":"oci://ghcr.io/keiailab/charts/mongodb-operator","last_tracking_errors":null}]' >"$out"
		else
			printf '[]' >"$out"
		fi
		;;
	*/packages/helm/keiailab-mongodb-operator/mongodb-operator/1.12.4)
		if [[ "${ARTIFACTHUB_TEST_CASE:-missing}" == "registered" ]]; then
			printf '{"name":"mongodb-operator","version":"1.12.4","app_version":"1.12.2","signed":true,"repository":{"url":"oci://ghcr.io/keiailab/charts/mongodb-operator"}}' >"$out"
		else
			exit 22
		fi
		;;
	*)
		echo "unexpected curl url: $url" >&2
		exit 99
		;;
esac
SH
chmod +x "$stubbin/curl"

export PATH="$stubbin:$PATH"
export CURL_BIN="$stubbin/curl"
export HELM_BIN="$stubbin/helm"
export ARTIFACTHUB_API_URL="https://artifacthub.test/api/v1"
export ARTIFACTHUB_ORG="keiailab"
export ARTIFACTHUB_REPOSITORY_NAME="keiailab-mongodb-operator"
export ARTIFACTHUB_PACKAGE_NAME="mongodb-operator"
export EXPECTED_ARTIFACTHUB_REPOSITORY_URL="oci://ghcr.io/keiailab/charts/mongodb-operator"
export EXPECTED_CHART_VERSION="1.12.4"
export EXPECTED_APP_VERSION="1.12.2"
export ARTIFACTHUB_SMOKE_SLEEP_SECONDS="0"
export HELM_REPO_URL="https://keiailab.github.io/mongodb-operator"

if ARTIFACTHUB_TEST_CASE=missing bash "$repo_root/hack/artifacthub_smoke.sh" >"$tmpdir/missing.out" 2>&1; then
	echo "expected missing repository case to fail" >&2
	exit 1
fi
grep -q "Artifact Hub repository is not registered" "$tmpdir/missing.out"

ARTIFACTHUB_TEST_CASE=registered bash "$repo_root/hack/artifacthub_smoke.sh" >"$tmpdir/registered.out" 2>&1
grep -q "Artifact Hub package OK" "$tmpdir/registered.out"

echo "artifacthub smoke shell test PASS"
