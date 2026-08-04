DROP TRIGGER IF EXISTS trg_fee_rule_version_overlap ON fee_rule_versions;
DROP FUNCTION IF EXISTS fn_validate_fee_rule_version_overlap();
DROP TABLE IF EXISTS fee_rule_versions;
ALTER TABLE fee_rules
    DROP CONSTRAINT IF EXISTS chk_fee_rules_flat_nonnegative,
    DROP CONSTRAINT IF EXISTS chk_fee_rules_status,
    DROP CONSTRAINT IF EXISTS chk_fee_rules_approved_distinct,
    DROP COLUMN IF EXISTS created_by,
    DROP COLUMN IF EXISTS approved_by,
    DROP COLUMN IF EXISTS rule_version,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS effective_from,
    DROP COLUMN IF EXISTS effective_until;
