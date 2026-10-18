-- журнал жизни импорта: загрузили, применили, протух, отменили...
CREATE TABLE import_events (
    id         bigserial PRIMARY KEY,
    import_id  bigint      NOT NULL REFERENCES imports (id) ON DELETE CASCADE,
    type       text        NOT NULL,
    message    text        NOT NULL DEFAULT '',
    details    jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX import_events_import_idx ON import_events (import_id, id);

-- что реально поменялось в каталоге и откуда. пишется в той же транзакции что и само изменение,
-- так что изменения без записи в истории быть не может
CREATE TABLE product_history (
    id            bigserial PRIMARY KEY,
    sku           text        NOT NULL, -- без FK специально: история должна пережить товар
    import_id     bigint REFERENCES imports (id),
    source        text        NOT NULL CHECK (source IN ('import', 'manual')),
    action        text        NOT NULL CHECK (action IN ('created', 'updated', 'deactivated')),
    old_state     jsonb,
    new_state     jsonb,
    version_after bigint      NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX product_history_sku_idx ON product_history (sku, id);
CREATE INDEX product_history_import_idx ON product_history (import_id, id) WHERE import_id IS NOT NULL;

-- для импортов которые были до этой миграции хоть что-то
INSERT INTO import_events (import_id, type, message, created_at)
SELECT id, status, 'восстановлено миграцией', created_at FROM imports;
