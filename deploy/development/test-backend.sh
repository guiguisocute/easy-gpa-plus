#!/bin/sh
set -eu

# Fixed local URLs come from the E2E overlay, never the daily development env.
case "${EASYGPA_TEST_ADMIN_DATABASE_URL:-}" in
  postgres://*@127.0.0.1:5432/easygpa_e2e\?*) ;;
  *) echo 'Go integration tests require the disposable easygpa_e2e database' >&2; exit 1 ;;
esac

export EASYGPA_TEST_CLASS_ID
EASYGPA_TEST_CLASS_ID="$(psql "$EASYGPA_TEST_ADMIN_DATABASE_URL" -X -qAt -v ON_ERROR_STOP=1 -f /workspace/deploy/development/test-fixture.sql)"
case "$EASYGPA_TEST_CLASS_ID" in
  ''|*[!0-9]*) echo 'Cannot create the synthetic Go test class' >&2; exit 1 ;;
esac
# Remote-backup tests deliberately permit loopback, not arbitrary private
# addresses. Forward only this local test endpoint without relaxing that guard.
socat TCP-LISTEN:3900,bind=127.0.0.1,reuseaddr,fork TCP:garage:3900 &
proxy_pid=$!
trap 'kill "$proxy_pid" 2>/dev/null || true; wait "$proxy_pid" 2>/dev/null || true' EXIT
go "$@"
