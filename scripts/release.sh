#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat <<'EOF'
Usage:
  scripts/release.sh VERSION [options]

Examples:
  scripts/release.sh v0.1.2
  scripts/release.sh 0.1.2 --no-push

Options:
  --branch NAME    Release branch to require before tagging (default: main)
  --remote NAME    Git remote to push to (default: origin)
  --no-push        Commit and tag locally without pushing
  --skip-tests     Skip go test ./...
  -h, --help       Show this help
EOF
}

die() {
	printf 'release: %s\n' "$*" >&2
	exit 1
}

run() {
	printf '+ %s\n' "$*"
	"$@"
}

normalize_version() {
	local input=$1
	if [[ $input =~ ^[0-9]+[.][0-9]+[.][0-9]+([-+][0-9A-Za-z.-]+)?$ ]]; then
		printf 'v%s\n' "$input"
		return 0
	fi
	printf '%s\n' "$input"
}

version=""
branch="main"
remote="origin"
push_release=1
run_tests=1

while [ "$#" -gt 0 ]; do
	case "$1" in
		--branch)
			shift
			[ "$#" -gt 0 ] || die "--branch requires a value"
			branch=$1
			;;
		--remote)
			shift
			[ "$#" -gt 0 ] || die "--remote requires a value"
			remote=$1
			;;
		--no-push)
			push_release=0
			;;
		--skip-tests)
			run_tests=0
			;;
		-h|--help)
			usage
			exit 0
			;;
		-*)
			die "unknown option: $1"
			;;
		*)
			[ -z "$version" ] || die "version specified more than once"
			version=$(normalize_version "$1")
			;;
	esac
	shift
done

[ -n "$version" ] || {
	usage >&2
	exit 2
}

[[ $version =~ ^v[0-9]+[.][0-9]+[.][0-9]+([-+][0-9A-Za-z.-]+)?$ ]] || \
	die "version must look like vX.Y.Z, for example v0.1.2"

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "${script_dir}/.." && pwd)
cd "$repo_root"

command -v git >/dev/null 2>&1 || die "git is required"
command -v go >/dev/null 2>&1 || die "go is required"
command -v perl >/dev/null 2>&1 || die "perl is required"

[ -f proxy.go ] || die "proxy.go not found; run from the tcptun repository"

current_branch=$(git rev-parse --abbrev-ref HEAD) || die "failed to read current branch"
[ "$current_branch" = "$branch" ] || die "current branch is $current_branch, expected $branch"

[ -z "$(git status --porcelain)" ] || die "working tree is dirty; commit or stash changes first"

run git fetch "$remote" --tags

local_head=$(git rev-parse HEAD) || die "failed to read local HEAD"
remote_head=$(git rev-parse "${remote}/${branch}") || die "failed to read ${remote}/${branch}"
[ "$local_head" = "$remote_head" ] || die "local ${branch} is not aligned with ${remote}/${branch}"

if git rev-parse -q --verify "refs/tags/${version}" >/dev/null; then
	die "local tag ${version} already exists"
fi

if git ls-remote --exit-code --tags "$remote" "refs/tags/${version}" >/dev/null 2>&1; then
	die "remote tag ${version} already exists on ${remote}"
fi

perl -0pi -e 's/(^\s*Version\s*=\s*")[^"]+(")/${1}'"${version}"'${2}/m' proxy.go

if ! VERSION_TO_VERIFY=$version perl -ne '$ok = 1 if /^\s*Version\s*=\s*"\Q$ENV{VERSION_TO_VERIFY}\E"/; END { exit($ok ? 0 : 1) }' proxy.go; then
	die "failed to update Version in proxy.go"
fi

if [ "$run_tests" -eq 1 ]; then
	run go test ./...
fi

run git add proxy.go
run git commit -m "chore: release ${version}"
run git tag -a "$version" -m "$version"

if [ "$push_release" -eq 1 ]; then
	run git push "$remote" "$branch"
	run git push "$remote" "$version"
	printf 'release: pushed %s and triggered the tag release workflow\n' "$version"
else
	printf 'release: created local commit and tag %s; push skipped\n' "$version"
fi
