#!/usr/bin/env bash
# Keep the first-contribution navigation map executable as the tree evolves.
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

failures=0

require_file() {
	local file="$1"
	if [[ ! -f "$file" ]]; then
		printf 'onboarding-check: missing %s\n' "$file" >&2
		failures=$((failures + 1))
	fi
}

require_text() {
	local file="$1"
	local pattern="$2"
	if ! rg --quiet --fixed-strings "$pattern" "$file"; then
		printf 'onboarding-check: %s does not contain %q\n' "$file" "$pattern" >&2
		failures=$((failures + 1))
	fi
}

# 1. Auth login handler.
require_file services/auth/internal/transport/http/http.go
require_text services/auth/internal/transport/http/http.go 'func (h *Handler) LoginHandler'

# 2. Payin top-up persistence.
require_file services/payin/internal/repository/topup_repository.go
require_text services/payin/internal/repository/topup_repository.go 'func (r *repo) InsertTopupIntent'

# 3. Vendor callback signature verification.
require_file services/vendor-service/internal/adapter/mockvendor/mockvendor.go
require_text services/vendor-service/internal/adapter/mockvendor/mockvendor.go 'func (v *Verifier) VerifyAndParse'

# 4. Ledger posting transaction boundary.
require_file services/ledger/internal/ledger/handle/service.go
require_text services/ledger/internal/ledger/handle/service.go 'func (s *Service) Handle'
require_text services/ledger/internal/ledger/handle/service.go 'func (s *Service) execTransfer'

# 5. A service-owned migration that creates a table.
require_file services/payin/migrations/000001_payin.up.sql
require_text services/payin/migrations/000001_payin.up.sql 'CREATE TABLE payin_webhook_events'

# The smallest useful test and the complete verification command must be
# discoverable from contributor-facing documentation.
require_text services/auth/README.md 'go test -run '\''^$'\'' ./services/auth/...'
require_text services/payin/README.md 'go test -run '\''^$'\'' ./services/payin/...'
require_text services/ledger/README.md 'go test -run '\''^$'\'' ./services/ledger/...'
require_text docs/development/onboarding.md 'make verify-full'

if (( failures > 0 )); then
	printf 'onboarding-check: %d navigation assertions failed\n' "$failures" >&2
	exit 1
fi

printf 'onboarding-check: critical ownership paths and contributor commands are discoverable\n'
