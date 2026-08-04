package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This source-level contract runs in the normal unit suite; the integration
// suite additionally applies the migration against PostgreSQL and attempts
// the forbidden raw-SQL transition under the app role.
func TestDisbursementSchemaRequiresApproval(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "services", "ledger", "migrations", "000045_disbursement_processing_requires_approval.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := string(contents)
	for _, required := range []string{
		"chk_disbursement_batches_processing_requires_approval",
		"approved_by IS NOT NULL",
		"approved_at IS NOT NULL",
		"trg_disbursement_approval_fields_immutable",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("disbursement invariant migration missing %q", required)
		}
	}
}
