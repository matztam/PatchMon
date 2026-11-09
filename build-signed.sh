#!/usr/bin/env bash
# Builds patchmon-server with minisign-signed agents.
#
# Usage: ./build-signed.sh [VERSION]
#   VERSION defaults to the nearest git tag (e.g. 2.1.8).
#
# Asks once for the minisign password, checks that the key matches the public
# key compiled into the agents in the field, builds, then verifies every
# agent signature before reporting success.
set -euo pipefail

PUBLIC_KEY=RWT0kfg3NkV4JfWsRD9Z4wtjOaG477mx3I89beQjtX53cUqTIeH85F0b
PRIVATE_KEY=$HOME/.minisign/patchmon.key

cd "$(dirname "$(readlink -f "$0")")"
export PATH=/usr/local/go/bin:$PATH

VERSION=${1:-$(git describe --tags --abbrev=0 | sed 's/^v//')}

die() { echo "error: $*" >&2; exit 1; }

[ -f "$PRIVATE_KEY" ] || die "private key not found: $PRIVATE_KEY"
command -v minisign >/dev/null || die "minisign is not installed"
command -v go >/dev/null || die "go is not installed (looked in /usr/local/go/bin and PATH)"

echo "==> Branch: $(git branch --show-current) @ $(git rev-parse --short HEAD)"
echo "==> Version: $VERSION"
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "warning: working tree has uncommitted changes" >&2
fi

# IFS= keeps leading and trailing spaces, which read would otherwise strip.
IFS= read -rsp 'minisign password: ' MINISIGN_PASSWORD; echo
export MINISIGN_PASSWORD

# Wrong password or wrong key fails here, before a ten-minute build.
# minisign -R refuses to overwrite an existing file, so give it a fresh path.
tmpdir=$(mktemp -d)
derived=$tmpdir/derived.pub
trap 'rm -rf "$tmpdir"; unset MINISIGN_PASSWORD' EXIT
if ! out=$(printf '%s\n' "$MINISIGN_PASSWORD" | minisign -R -s "$PRIVATE_KEY" -p "$derived" 2>&1); then
  echo "$out" >&2
  die "could not unlock $PRIVATE_KEY (password read: ${#MINISIGN_PASSWORD} characters)"
fi
[ "$(tail -1 "$derived")" = "$PUBLIC_KEY" ] \
  || die "$PRIVATE_KEY does not match the public key in the deployed agents"
echo "==> Key unlocked and matches $PUBLIC_KEY"

export PATCHMON_SIGNING_PUBLIC_KEY=$PUBLIC_KEY PATCHMON_SIGNING_PRIVATE_KEY=$PRIVATE_KEY
./docker/build.sh --version "$VERSION" --tag "$VERSION"

echo "==> Verifying agent signatures"
count=0
for f in agents-prebuilt/patchmon-agent-*; do
  case "$f" in *.minisig) continue ;; esac
  minisign -Vqm "$f" -P "$PUBLIC_KEY" -x "$f.minisig" >/dev/null \
    || die "bad signature: $f"
  comment=$(sed -n 3p "$f.minisig")
  [ "$comment" = "trusted comment: PatchMon Agent $VERSION" ] \
    || die "$f.minisig has '$comment', expected version $VERSION"
  count=$((count + 1))
done
[ "$count" -eq 10 ] || die "expected 10 agents, found $count"

echo "==> Done: patchmon-server:$VERSION with $count signed agents"
