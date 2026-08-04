-- Defense in depth for maker-checker disbursements: an application bug or
-- raw SQL session must not make a batch executable without a distinct checker.
UPDATE disbursement_batches
SET approved_by = CASE WHEN created_by = 'legacy-migration' THEN 'schema-migration' ELSE 'legacy-migration' END,
    approved_at = COALESCE(approved_at, created_at)
WHERE status IN ('processing', 'completed', 'completed_with_errors')
  AND (approved_by IS NULL OR approved_at IS NULL);

ALTER TABLE disbursement_batches
    ADD CONSTRAINT chk_disbursement_batches_processing_requires_approval
    CHECK (
        status NOT IN ('processing', 'completed', 'completed_with_errors')
        OR (approved_by IS NOT NULL AND approved_at IS NOT NULL)
    );

CREATE OR REPLACE FUNCTION fn_disbursement_approval_fields_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IN ('processing', 'completed', 'completed_with_errors') AND
       (NEW.approved_by IS DISTINCT FROM OLD.approved_by OR
        NEW.approved_at IS DISTINCT FROM OLD.approved_at) THEN
        RAISE EXCEPTION 'approval fields cannot change after disbursement processing begins';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_disbursement_approval_fields_immutable
    BEFORE UPDATE ON disbursement_batches
    FOR EACH ROW EXECUTE FUNCTION fn_disbursement_approval_fields_immutable();
