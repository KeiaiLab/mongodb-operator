#!/usr/bin/env bash
# publish-devto.sh — Publish the launch blog post to dev.to via the Forem API.
#
# Usage:
#   DEV_API_KEY=<your-key> ./scripts/publish-devto.sh [--publish]
#
#   Without --publish, the article is created as a draft (published: false).
#   With --publish, it goes live immediately.
#
# Get an API key at: https://dev.to/settings/extensions (DEV Community API Keys)
#
# Idempotent: checks existing articles by title; updates instead of duplicating.

set -euo pipefail

POST_FILE="docs/blog/dev-to-launch-post.md"
API="https://dev.to/api"
TITLE="We Built an Open-Source MongoDB Operator Because Neither Percona Nor the Community Operator Was Enough"

if [ -z "${DEV_API_KEY:-}" ]; then
  echo "ERROR: DEV_API_KEY environment variable not set." >&2
  echo "  Get a key at https://dev.to/settings/extensions and run:" >&2
  echo "  DEV_API_KEY=<key> $0 [--publish]" >&2
  exit 1
fi

if [ ! -f "$POST_FILE" ]; then
  echo "ERROR: $POST_FILE not found. Run from repo root." >&2
  exit 1
fi

PUBLISH="false"
if [ "${1:-}" = "--publish" ]; then
  PUBLISH="true"
fi

# Build the article JSON payload from the markdown file's body (frontmatter kept —
# dev.to parses its own frontmatter when sent as body_markdown).
BODY_MARKDOWN=$(cat "$POST_FILE")

PAYLOAD=$(jq -n \
  --arg body "$BODY_MARKDOWN" \
  --argjson published "$PUBLISH" \
  '{article: {body_markdown: $body, published: $published}}')

# Check for an existing article with the same title (idempotency).
EXISTING_ID=$(curl -sS -H "api-key: $DEV_API_KEY" "$API/articles/me/all?per_page=100" \
  | jq -r --arg t "$TITLE" '.[] | select(.title == $t) | .id' | head -1)

if [ -n "$EXISTING_ID" ]; then
  echo "Updating existing article id=$EXISTING_ID ..."
  RESP=$(curl -sS -X PUT -H "api-key: $DEV_API_KEY" -H "Content-Type: application/json" \
    -d "$PAYLOAD" "$API/articles/$EXISTING_ID")
else
  echo "Creating new article ..."
  RESP=$(curl -sS -X POST -H "api-key: $DEV_API_KEY" -H "Content-Type: application/json" \
    -d "$PAYLOAD" "$API/articles")
fi

URL=$(echo "$RESP" | jq -r '.url // empty')
if [ -n "$URL" ]; then
  echo "Done: $URL (published=$PUBLISH)"
else
  echo "Response:" >&2
  echo "$RESP" | jq . >&2
  exit 1
fi
