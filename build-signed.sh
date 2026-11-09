#!/usr/bin/env bash
# Builds PatchMon with minisign-signed agents.
#
# Usage: ./build-signed.sh [--agents] [VERSION]
#   VERSION defaults to the nearest git tag (e.g. 2.1.8).
#
#   (default)  Builds the whole patchmon-server image locally.
#   --agents   Builds and signs only the agents and pushes them, with their
#              .minisig files, as an OCI artifact to PATCHMON_AGENTS_REPO:VERSION.
#              A CI job can then build the image from them after verifying the
#              signatures, so the private key never leaves this machine.
#
# Configuration, from the environment or from the file named by
# PATCHMON_BUILD_ENV (default: ~/.config/patchmon/build.env):
#   PATCHMON_SIGNING_PUBLIC_KEY   public key compiled into the agents (RW...)
#   PATCHMON_SIGNING_PRIVATE_KEY  path to the matching minisign secret key
#   PATCHMON_AGENTS_REPO          registry/repository for --agents,
#                                 e.g. registry.example.com/patchmon-agents
#
# Asks once for the minisign password, checks that the key unlocks and matches
# the public key before building, then verifies every agent signature.
set -euo pipefail

ORAS_IMAGE=ghcr.io/oras-project/oras:v1.2.0

cd "$(dirname "$(readlink -f "$0")")"
export PATH=/usr/local/go/bin:$PATH

die() { echo "error: $*" >&2; exit 1; }

CONFIG=${PATCHMON_BUILD_ENV:-$HOME/.config/patchmon/build.env}
if [ -f "$CONFIG" ]; then
  # shellcheck disable=SC1090
  . "$CONFIG"
fi

MODE=image
if [ "${1:-}" = "--agents" ]; then
  MODE=agents
  shift
fi
VERSION=${1:-$(git describe --tags --abbrev=0 | sed 's/^v//')}

PUBLIC_KEY=${PATCHMON_SIGNING_PUBLIC_KEY:-}
PRIVATE_KEY=${PATCHMON_SIGNING_PRIVATE_KEY:-}
[ -n "$PUBLIC_KEY" ] || die "PATCHMON_SIGNING_PUBLIC_KEY is not set (see the header of $0)"
[ -n "$PRIVATE_KEY" ] || die "PATCHMON_SIGNING_PRIVATE_KEY is not set (see the header of $0)"
[ -f "$PRIVATE_KEY" ] || die "private key not found: $PRIVATE_KEY"
command -v minisign >/dev/null || die "minisign is not installed"
command -v go >/dev/null || die "go is not installed (looked in /usr/local/go/bin and PATH)"

if [ "$MODE" = agents ]; then
  AGENTS_REPO=${PATCHMON_AGENTS_REPO:-}
  [ -n "$AGENTS_REPO" ] || die "PATCHMON_AGENTS_REPO is not set (needed for --agents)"
  REGISTRY=${AGENTS_REPO%%/*}
fi

echo "==> Branch: $(git branch --show-current) @ $(git rev-parse --short HEAD)"
echo "==> Version: $VERSION"
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "warning: working tree has uncommitted changes" >&2
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"; unset MINISIGN_PASSWORD' EXIT

# Runs oras with a registry login that holds only the target registry's entry
# and is readable only by us.
oras() {
  docker run --rm --network host --user "$(id -u):$(id -g)" \
    -v "$tmpdir/dockerconfig":/dockerconfig:ro \
    -v "$PWD":/work -w "/work/${ORAS_CWD:-}" \
    "$ORAS_IMAGE" "$@" --registry-config /dockerconfig/config.json
}
if [ "$MODE" = agents ]; then
  mkdir -m 700 "$tmpdir/dockerconfig"
  python3 -I -c '
import json, os, sys
c = json.load(open(os.path.expanduser("~/.docker/config.json")))
auth = c.get("auths", {}).get(sys.argv[1])
if not auth:
    sys.exit("no docker login for " + sys.argv[1] + " (run: docker login " + sys.argv[1] + ")")
json.dump({"auths": {sys.argv[1]: auth}}, open(sys.argv[2], "w"))
' "$REGISTRY" "$tmpdir/dockerconfig/config.json"
  # Published agents are what CI trusts; never replace them silently.
  if oras resolve "$AGENTS_REPO:$VERSION" >/dev/null 2>&1; then
    die "$AGENTS_REPO:$VERSION already exists; delete it first to republish"
  fi
fi

# IFS= keeps leading and trailing spaces, which read would otherwise strip.
IFS= read -rsp 'minisign password: ' MINISIGN_PASSWORD; echo
export MINISIGN_PASSWORD

# Wrong password or wrong key fails here, before a ten-minute build.
# minisign -R refuses to overwrite an existing file, so give it a fresh path.
derived=$tmpdir/derived.pub
if ! out=$(printf '%s\n' "$MINISIGN_PASSWORD" | minisign -R -s "$PRIVATE_KEY" -p "$derived" 2>&1); then
  echo "$out" >&2
  die "could not unlock $PRIVATE_KEY (password read: ${#MINISIGN_PASSWORD} characters)"
fi
[ "$(tail -1 "$derived")" = "$PUBLIC_KEY" ] \
  || die "$PRIVATE_KEY does not match PATCHMON_SIGNING_PUBLIC_KEY"
echo "==> Key unlocked and matches $PUBLIC_KEY"

export PATCHMON_SIGNING_PUBLIC_KEY=$PUBLIC_KEY PATCHMON_SIGNING_PRIVATE_KEY=$PRIVATE_KEY

if [ "$MODE" = agents ]; then
  AGENTS_DIR=agent-source-code/build
  rm -f "$AGENTS_DIR"/patchmon-agent-*
  make -C agent-source-code build-linux build-freebsd build-windows VERSION="$VERSION"
  make -C agent-source-code sign-all VERSION="$VERSION"
else
  AGENTS_DIR=agents-prebuilt
  ./docker/build.sh --version "$VERSION" --tag "$VERSION"
fi
unset MINISIGN_PASSWORD

echo "==> Verifying agent signatures"
count=0
for f in "$AGENTS_DIR"/patchmon-agent-*; do
  case "$f" in *.minisig) continue ;; esac
  minisign -Vqm "$f" -P "$PUBLIC_KEY" -x "$f.minisig" >/dev/null \
    || die "bad signature: $f"
  comment=$(sed -n 3p "$f.minisig")
  [ "$comment" = "trusted comment: PatchMon Agent $VERSION" ] \
    || die "$f.minisig has '$comment', expected version $VERSION"
  count=$((count + 1))
done
[ "$count" -eq 10 ] || die "expected 10 agents, found $count"

if [ "$MODE" = agents ]; then
  echo "==> Uploading to $AGENTS_REPO:$VERSION"
  # Pushed from inside the directory so the artifact stores bare file names.
  # shellcheck disable=SC2046
  ORAS_CWD=$AGENTS_DIR oras push --artifact-type application/vnd.patchmon.agents.v1 \
    "$AGENTS_REPO:$VERSION" $(cd "$AGENTS_DIR" && ls patchmon-agent-*) >/dev/null
  echo "==> Done: $count signed agents at $AGENTS_REPO:$VERSION ($(oras resolve "$AGENTS_REPO:$VERSION"))"
else
  echo "==> Done: patchmon-server:$VERSION with $count signed agents"
fi
