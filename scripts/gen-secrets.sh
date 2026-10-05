#!/usr/bin/env bash
# Generates random S3 identities for local/CI use and writes them to
# gitignored files under .secrets/. Never commit the output of this script.
set -euo pipefail

OUT_DIR="${1:-.secrets}"
mkdir -p "$OUT_DIR"

rand() { ( set +o pipefail; tr -dc 'A-Za-z0-9' </dev/urandom | head -c "$1" ); }

ADMIN_AK="AKIA$(rand 16)"
ADMIN_SK="$(rand 40)"
RO_AK="AKIA$(rand 16)"
RO_SK="$(rand 40)"
SCOPED_AK="AKIA$(rand 16)"
SCOPED_SK="$(rand 40)"

cat > "$OUT_DIR/seaweedfs_s3_config.json" << JSON
{
  "identities": [
    {
      "name": "harness-admin",
      "credentials": [{"accessKey": "$ADMIN_AK", "secretKey": "$ADMIN_SK"}],
      "actions": ["Admin", "Read", "Write", "List", "Tagging"]
    },
    {
      "name": "harness-readonly",
      "credentials": [{"accessKey": "$RO_AK", "secretKey": "$RO_SK"}],
      "actions": ["Read", "List"]
    },
    {
      "name": "harness-scoped",
      "credentials": [{"accessKey": "$SCOPED_AK", "secretKey": "$SCOPED_SK"}],
      "actions": ["Read:it-scoped*", "Write:it-scoped*", "List:it-scoped*"]
    }
  ]
}
JSON

cat > "$OUT_DIR/compose.env" << ENV
SWFSIT_TARGET_NAME=seaweedfs-compose
SWFSIT_ENDPOINT=http://127.0.0.1:8333
SWFSIT_REGION=us-east-1
SWFSIT_PATH_STYLE=true
SWFSIT_ACCESS_KEY=$ADMIN_AK
SWFSIT_SECRET_KEY=$ADMIN_SK
SWFSIT_RO_ACCESS_KEY=$RO_AK
SWFSIT_RO_SECRET_KEY=$RO_SK
SWFSIT_SCOPED_ACCESS_KEY=$SCOPED_AK
SWFSIT_SCOPED_SECRET_KEY=$SCOPED_SK
SWFSIT_SCOPED_BUCKET=it-scoped
SWFSIT_VERSIONING=true
SWFSIT_DISRUPTOR=false
SWFSIT_MAX_OBJECT_MIB=100
ENV

echo "Wrote $OUT_DIR/seaweedfs_s3_config.json and $OUT_DIR/compose.env (gitignored; never commit)."
