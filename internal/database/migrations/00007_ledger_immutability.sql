-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION prevent_credit_ledger_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'credit ledger is append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER credit_ledger_prevent_update_delete
BEFORE UPDATE OR DELETE ON credit_ledger
FOR EACH ROW EXECUTE FUNCTION prevent_credit_ledger_mutation();

-- +goose Down
DROP TRIGGER credit_ledger_prevent_update_delete ON credit_ledger;
DROP FUNCTION prevent_credit_ledger_mutation();
