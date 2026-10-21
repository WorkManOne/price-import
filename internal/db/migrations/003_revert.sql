-- откат примененного импорта
ALTER TABLE imports ADD COLUMN reverted_at timestamptz;

ALTER TABLE imports DROP CONSTRAINT imports_status_check;
ALTER TABLE imports ADD CONSTRAINT imports_status_check
    CHECK (status IN ('rejected', 'analyzed', 'applied', 'stale', 'canceled', 'reverted'));

ALTER TABLE product_history DROP CONSTRAINT product_history_source_check;
ALTER TABLE product_history ADD CONSTRAINT product_history_source_check
    CHECK (source IN ('import', 'manual', 'revert'));

-- deleted - откат импорта, который этот товар создал. version_after = 0
ALTER TABLE product_history DROP CONSTRAINT product_history_action_check;
ALTER TABLE product_history ADD CONSTRAINT product_history_action_check
    CHECK (action IN ('created', 'updated', 'deactivated', 'deleted'));
