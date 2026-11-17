#!/usr/bin/env bash
# Sanity-checks the two .deb files (server and agent) of one architecture:
# file names, control fields, the files they install, the maintainer scripts and, when this machine
# can run them, the version the packaged binary reports.
#
#   verify-debs.sh <dir> <version> <arch>      e.g.  verify-debs.sh dist 1.2.3 amd64
set -euo pipefail

dir="${1:?usage: verify-debs.sh <dir> <version> <arch>}"
version="${2:?usage: verify-debs.sh <dir> <version> <arch>}"
arch="${3:?usage: verify-debs.sh <dir> <version> <arch>}"

fail() { echo "FAIL: $*" >&2; exit 1; }

host_arch="$(dpkg --print-architecture 2>/dev/null || echo unknown)"

for role in server agent; do
    pkg="lsd-$role"
    deb="$dir/${pkg}_${version}_${arch}.deb"
    [ -f "$deb" ] || fail "missing $deb"

    for field_expect in "Package=$pkg" "Version=$version" "Architecture=$arch"; do
        field="${field_expect%%=*}"
        want="${field_expect#*=}"
        got="$(dpkg-deb -f "$deb" "$field")"
        [ "$got" = "$want" ] || fail "$deb: $field is '$got', expected '$want'"
    done

    contents="$(dpkg-deb -c "$deb")"
    for path in "./opt/lsd/$role/lsd" "./lib/systemd/system/$pkg.service" "./etc/lsd/$role.env"; do
        grep -q " ${path}\$" <<<"$contents" || fail "$deb does not install $path"
    done

    control_files="$(dpkg-deb --ctrl-tarfile "$deb" | tar -t)"
    for script in postinst prerm postrm conffiles; do
        grep -q "^\./${script}\$" <<<"$control_files" || fail "$deb has no $script"
    done

    # The binary can only be run here if the package is for this machine's architecture.
    if [ "$arch" = "$host_arch" ]; then
        tmp="$(mktemp -d)"
        trap 'rm -rf "$tmp"' EXIT
        dpkg-deb -x "$deb" "$tmp"
        got="$("$tmp/opt/lsd/$role/lsd" version)"
        [ "$got" = "$version" ] || fail "$deb: the binary reports version '$got', expected '$version'"
        echo "ok   $deb (binary reports $got)"
    else
        echo "ok   $deb (not run: $arch binary on a $host_arch machine)"
    fi
done
