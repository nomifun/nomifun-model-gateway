#!/usr/bin/env bash
# Copyright 2026 NomiFun Model Gateway contributors
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

nmg_repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$nmg_repository_root"
nmg_go_licenses_version='v2.0.1'
nmg_allowed_licenses='Apache-2.0,MIT,BSD-2-Clause,BSD-3-Clause,0BSD,ISC,Unlicense,CC0-1.0'

# Audit every source manifest and every locked dependency. Unsupported or
# incomplete frontend lockfiles fail closed; M0 has no frontend dependencies.
if ! command -v node >/dev/null 2>&1; then
  printf '%s\n' 'Node.js 24 or later is required for the frontend dependency license gate.' >&2
  exit 1
fi
node "$nmg_repository_root/scripts/check-frontend-licenses.mjs" "$nmg_repository_root"

if ! command -v go >/dev/null 2>&1; then
  printf '%s\n' 'Go is required. Install the version pinned in go.mod and add it to PATH.' >&2
  exit 1
fi

nmg_tool_bin="$nmg_repository_root/.tools/bin"
mkdir -p -- "$nmg_tool_bin"
GOBIN="$nmg_tool_bin" go install "github.com/google/go-licenses/v2@$nmg_go_licenses_version"
"$nmg_tool_bin/go-licenses" check --include_tests "--allowed_licenses=$nmg_allowed_licenses" ./...
printf '%s\n' 'Go license gate passed, including imported transitive and test dependencies.'
