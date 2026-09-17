#!/usr/bin/env bash
# Smoke test: a quick, scriptable walk through the backend's main features
# against a real running instance (local or deployed). It is NOT a
# replacement for the Go/TypeScript test suites — it just proves the whole
# system is wired together correctly end to end: the server is reachable,
# the database is working, media can be created, playlists can be changed,
# and sync playback actually reaches every window.
#
# Usage:
#   scripts/smoke_test.sh <BASE_URL>
#   scripts/smoke_test.sh http://localhost:8099
#   scripts/smoke_test.sh https://eva2-backend.onrender.com
#
# Exits 0 if every check passes, 1 on the first failure.

set -u

BASE_URL="${1:-}"
if [ -z "$BASE_URL" ]; then
  echo "Usage: $0 <BASE_URL>" >&2
  exit 1
fi
# Strip a trailing slash so "$BASE_URL/health" never ends up as "...//health".
BASE_URL="${BASE_URL%/}"

PASS_COUNT=0
FAIL_COUNT=0

# ---- small helpers -----------------------------------------------------

# extract_json_string FIELD JSON
# Pulls the first "field":"value" match out of a JSON blob using plain
# grep/sed rather than a dependency like jq, since this script needs to run
# anywhere with just curl and coreutils (per the project's Git Bash
# requirement).
extract_json_string() {
  local field="$1" json="$2"
  echo "$json" | grep -o "\"${field}\":\"[^\"]*\"" | head -1 | sed -E "s/\"${field}\":\"([^\"]*)\"/\1/"
}

extract_json_bool() {
  local field="$1" json="$2"
  echo "$json" | grep -o "\"${field}\":\(true\|false\)" | head -1 | sed -E "s/\"${field}\"://"
}

pass() {
  echo "  PASS: $1"
  PASS_COUNT=$((PASS_COUNT + 1))
}

fail() {
  echo "  FAIL: $1" >&2
  FAIL_COUNT=$((FAIL_COUNT + 1))
}

# expect_status NAME EXPECTED ACTUAL
expect_status() {
  local name="$1" expected="$2" actual="$3"
  if [ "$actual" = "$expected" ]; then
    pass "$name (status $actual)"
  else
    fail "$name — expected status $expected, got $actual"
  fi
}

echo "=== Smoke test against $BASE_URL ==="

# ---- 1. health -----------------------------------------------------------
# This first request doubles as a reachability check for the whole script.
# If the server can't be reached at all (wrong URL, backend not running,
# DNS failure, connection refused, timeout), curl itself fails (a non-zero
# exit code) and/or reports status "000" — every other check below would
# then fail too, for the exact same underlying reason, producing a wall of
# confusing "expected 200/201/204, got 000" noise. So this one case gets
# its own clear message and an immediate exit instead of a cascade.
echo ""
echo "-- GET /health"
HEALTH_STATUS=$(curl -s -o /tmp/smoke_health.json -w "%{http_code}" "$BASE_URL/health")
CURL_EXIT=$?

if [ "$CURL_EXIT" -ne 0 ] || [ "$HEALTH_STATUS" = "000" ]; then
  echo "Cannot reach $BASE_URL — is the backend running?" >&2
  rm -f /tmp/smoke_health.json
  exit 1
fi

expect_status "health check" "200" "$HEALTH_STATUS"
cat /tmp/smoke_health.json
echo ""

# ---- 2. state --------------------------------------------------------------
echo ""
echo "-- GET /api/state"
STATE_STATUS=$(curl -s -o /tmp/smoke_state.json -w "%{http_code}" "$BASE_URL/api/state")
expect_status "get state" "200" "$STATE_STATUS"
STATE_BODY=$(cat /tmp/smoke_state.json)

# Window objects are the only ones in this payload shaped like
# "id":"...","name":"...","cycle_anchor":"...", so this pattern reliably
# picks out window ids without needing a real JSON parser.
WINDOW_IDS=$(echo "$STATE_BODY" | grep -o '"id":"[^"]*","name":"[^"]*","cycle_anchor"' | sed -E 's/"id":"([^"]*)".*/\1/')
FIRST_WINDOW=$(echo "$WINDOW_IDS" | head -1)

if [ -z "$FIRST_WINDOW" ]; then
  fail "no windows found in /api/state — cannot continue with playlist/sync checks"
  echo ""
  echo "=== $PASS_COUNT passed, $FAIL_COUNT failed ==="
  exit 1
fi
pass "found windows: $(echo "$WINDOW_IDS" | tr '\n' ' ')"

# ---- 3. add media ----------------------------------------------------------
echo ""
echo "-- POST /api/media"
MEDIA_STATUS=$(curl -s -o /tmp/smoke_media.json -w "%{http_code}" -X POST "$BASE_URL/api/media" \
  -H "Content-Type: application/json" \
  -d '{"name":"Smoke Test Image","type":"image","url":"https://placehold.co/400x300.jpg?text=Smoke","duration_seconds":5}')
expect_status "create media" "201" "$MEDIA_STATUS"
MEDIA_BODY=$(cat /tmp/smoke_media.json)
MEDIA_ID=$(extract_json_string "id" "$MEDIA_BODY")

if [ -z "$MEDIA_ID" ]; then
  fail "no media id returned from POST /api/media — cannot continue"
  echo ""
  echo "=== $PASS_COUNT passed, $FAIL_COUNT failed ==="
  exit 1
fi
pass "created media $MEDIA_ID"

# ---- 4. add item to a window's playlist ------------------------------------
echo ""
echo "-- POST /api/windows/$FIRST_WINDOW/items"
ADD_ITEM_STATUS=$(curl -s -o /tmp/smoke_additem.json -w "%{http_code}" -X POST \
  "$BASE_URL/api/windows/$FIRST_WINDOW/items" \
  -H "Content-Type: application/json" \
  -d "{\"media_id\":\"$MEDIA_ID\"}")
expect_status "add playlist item" "201" "$ADD_ITEM_STATUS"
ADD_ITEM_BODY=$(cat /tmp/smoke_additem.json)
EFFECTIVE_AT=$(extract_json_string "effective_at" "$ADD_ITEM_BODY")
if [ -n "$EFFECTIVE_AT" ]; then
  pass "playlist change scheduled, effective_at=$EFFECTIVE_AT"
else
  fail "no effective_at returned from add-item response"
fi

# ---- 5. validation error check ---------------------------------------------
echo ""
echo "-- POST /api/media with an invalid type (expect 400)"
BAD_MEDIA_STATUS=$(curl -s -o /tmp/smoke_badmedia.json -w "%{http_code}" -X POST "$BASE_URL/api/media" \
  -H "Content-Type: application/json" \
  -d '{"name":"Bad","type":"not-a-real-type","duration_seconds":5}')
expect_status "reject invalid media type" "400" "$BAD_MEDIA_STATUS"

# ---- 6. start sync ----------------------------------------------------------
echo ""
echo "-- POST /api/sync"
SYNC_DURATION=20
SYNC_STATUS=$(curl -s -o /tmp/smoke_sync.json -w "%{http_code}" -X POST "$BASE_URL/api/sync" \
  -H "Content-Type: application/json" \
  -d "{\"media_id\":\"$MEDIA_ID\",\"duration_seconds\":$SYNC_DURATION}")
expect_status "create sync" "201" "$SYNC_STATUS"

# The backend's default SYNC_LEAD_MS is 1500ms — wait comfortably past that
# so the sync has actually started before we check /now on every window.
echo "   waiting 3s for the sync's lead time to pass..."
sleep 3

# ---- 7. confirm every window reports the synced media ----------------------
echo ""
echo "-- GET /api/windows/{id}/now for every window (expect synced_playback=true, media_id=$MEDIA_ID)"
ALL_SYNCED=true
while IFS= read -r WINDOW_ID; do
  [ -z "$WINDOW_ID" ] && continue
  NOW_BODY=$(curl -s "$BASE_URL/api/windows/$WINDOW_ID/now")
  SYNCED=$(extract_json_bool "synced_playback" "$NOW_BODY")
  NOW_MEDIA_ID=$(extract_json_string "media_id" "$NOW_BODY")
  if [ "$SYNCED" = "true" ] && [ "$NOW_MEDIA_ID" = "$MEDIA_ID" ]; then
    pass "window $WINDOW_ID is showing the synced media"
  else
    fail "window $WINDOW_ID — expected synced_playback=true and media_id=$MEDIA_ID, got synced_playback=$SYNCED media_id=$NOW_MEDIA_ID"
    ALL_SYNCED=false
  fi
done <<< "$WINDOW_IDS"

# ---- 8. cancel sync ----------------------------------------------------------
echo ""
echo "-- DELETE /api/sync"
CANCEL_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "$BASE_URL/api/sync")
expect_status "cancel sync" "204" "$CANCEL_STATUS"

echo ""
echo "-- GET /api/windows/$FIRST_WINDOW/now (expect synced_playback=false after cancel)"
AFTER_CANCEL_BODY=$(curl -s "$BASE_URL/api/windows/$FIRST_WINDOW/now")
AFTER_CANCEL_SYNCED=$(extract_json_bool "synced_playback" "$AFTER_CANCEL_BODY")
if [ "$AFTER_CANCEL_SYNCED" = "false" ]; then
  pass "window resumed normal playback after cancel"
else
  fail "window still reports synced_playback=$AFTER_CANCEL_SYNCED after cancelling sync"
fi

rm -f /tmp/smoke_health.json /tmp/smoke_state.json /tmp/smoke_media.json /tmp/smoke_additem.json /tmp/smoke_badmedia.json /tmp/smoke_sync.json

echo ""
echo "=== $PASS_COUNT passed, $FAIL_COUNT failed ==="
[ "$FAIL_COUNT" -eq 0 ]
