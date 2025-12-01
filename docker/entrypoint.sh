#!/bin/sh
# NornicDB Docker Entrypoint

set -e

# Build command line args from environment
ARGS="serve"
ARGS="$ARGS --data-dir=${NORNICDB_DATA_DIR:-/data}"
ARGS="$ARGS --http-port=${NORNICDB_HTTP_PORT:-7474}"
ARGS="$ARGS --bolt-port=${NORNICDB_BOLT_PORT:-7687}"

[ "${NORNICDB_NO_AUTH:-false}" = "true" ] && ARGS="$ARGS --no-auth"

# Embedding config
[ -n "$NORNICDB_EMBEDDING_URL" ] && ARGS="$ARGS --embedding-url=$NORNICDB_EMBEDDING_URL"
[ -n "$NORNICDB_EMBEDDING_MODEL" ] && ARGS="$ARGS --embedding-model=$NORNICDB_EMBEDDING_MODEL"
[ -n "$NORNICDB_EMBEDDING_DIM" ] && ARGS="$ARGS --embedding-dim=$NORNICDB_EMBEDDING_DIM"

exec /app/nornicdb $ARGS "$@"
