#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'build-androidbridge: %s\n' "$*" >&2
  exit 1
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"

command -v go >/dev/null 2>&1 || die "go is required but was not found in PATH"
command -v gomobile >/dev/null 2>&1 || die "gomobile is required. Install it with: go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init"

sdk_root="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}"
if [[ -z "${sdk_root}" ]]; then
  die "ANDROID_HOME or ANDROID_SDK_ROOT must point to your Android SDK"
fi
if [[ ! -d "${sdk_root}" ]]; then
  die "Android SDK directory does not exist: ${sdk_root}"
fi
if [[ ! -d "${sdk_root}/platforms" ]] || ! compgen -G "${sdk_root}/platforms/android-*" >/dev/null; then
  die "Android SDK platforms are missing under ${sdk_root}/platforms. Install at least one Android platform with sdkmanager."
fi

ndk_root="${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-}}"
if [[ -z "${ndk_root}" ]]; then
  if compgen -G "${sdk_root}/ndk/*" >/dev/null; then
    ndk_root="$(find "${sdk_root}/ndk" -mindepth 1 -maxdepth 1 -type d | sort | tail -n 1)"
  elif [[ -d "${sdk_root}/ndk-bundle" ]]; then
    ndk_root="${sdk_root}/ndk-bundle"
  fi
fi
if [[ -z "${ndk_root}" ]] || [[ ! -d "${ndk_root}" ]]; then
  die "Android NDK was not found. Set ANDROID_NDK_HOME or install the SDK ndk package."
fi

aar_out="${ANDROIDBRIDGE_AAR_OUT:-${repo_root}/dist/androidbridge.aar}"
target="${ANDROID_TARGET:-android}"
android_api="${ANDROID_API:-}"

mkdir -p "$(dirname "${aar_out}")"

args=(bind -target="${target}" -o "${aar_out}")
if [[ -n "${android_api}" ]]; then
  args+=(-androidapi "${android_api}")
fi
args+=("${repo_root}/mobile/androidbridge")

printf 'Building Android bridge AAR: %s\n' "${aar_out}"
(
  cd "${repo_root}"
  ANDROID_HOME="${sdk_root}" ANDROID_NDK_HOME="${ndk_root}" gomobile "${args[@]}"
)
printf 'Done: %s\n' "${aar_out}"
