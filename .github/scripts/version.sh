#!/usr/bin/env bash
# Prints the package version for this CI run.
#
#   tag push v1.2.3       ->  1.2.3
#   tag push v1.2.3-rc1   ->  1.2.3~rc1    (Debian's pre-release marker: sorts before 1.2.3)
#   anything else         ->  0.0.0+git<short sha>    (builds that are never published)
set -euo pipefail

if [ "${GITHUB_REF_TYPE:-}" = "tag" ]; then
    tag="${GITHUB_REF_NAME:?GITHUB_REF_NAME is not set}"
    if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
        echo "error: the tag '$tag' must look like v1.2.3 or v1.2.3-rc1" >&2
        exit 1
    fi
    # only the first '-' (the pre-release separator) becomes '~'. sed, because a '~' in a bash
    # ${var/-/~} replacement is subject to tilde expansion.
    # shellcheck disable=SC2001
    echo "${tag#v}" | sed 's/-/~/'
else
    sha="${GITHUB_SHA:-0000000}"
    echo "0.0.0+git${sha:0:7}"
fi
