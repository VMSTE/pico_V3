#!/bin/bash
packages=(
  "seroval"
  "nanoid"
  "fast-uri"
  "js-yaml"
  "brace-expansion"
  "hono"
  "@hono/node-server"
  "postcss"
  "ip-address"
  "lodash-es"
)

for pkg in "${packages[@]}"; do
  echo "=== PACKAGE: $pkg ==="
  pnpm why "$pkg" 2>&1
done
