-- каталог. sku храним уже нормализованным (верхний регистр, без пробелов по краям)
CREATE TABLE products (
    sku           text PRIMARY KEY,
    name          text        NOT NULL,
    category      text        NOT NULL DEFAULT '',
    price_kopecks bigint      NOT NULL CHECK (price_kopecks >= 0),
    stock         bigint      NOT NULL DEFAULT 0 CHECK (stock >= 0),
    active        boolean     NOT NULL DEFAULT true,
    -- версия растет на каждое изменение, по ней ловим что отчет устарел
    version       bigint      NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX products_category_idx ON products (category);

CREATE TABLE imports (
    id              bigserial PRIMARY KEY,
    status          text        NOT NULL CHECK (status IN ('rejected', 'analyzed', 'applied', 'stale', 'canceled', 'reverted')),
    mode            text        NOT NULL CHECK (mode IN ('partial', 'full')),
    filename        text        NOT NULL DEFAULT '',
    file_sha256     text        NOT NULL,
    file_size       bigint      NOT NULL,
    encoding        text        NOT NULL DEFAULT '',
    delimiter       text        NOT NULL DEFAULT '',
    columns         text[]      NOT NULL DEFAULT '{}',
    ignored_columns text[]      NOT NULL DEFAULT '{}',
    total_rows      int         NOT NULL DEFAULT 0,
    new_count       int         NOT NULL DEFAULT 0,
    changed_count   int         NOT NULL DEFAULT 0,
    unchanged_count int         NOT NULL DEFAULT 0,
    invalid_count   int         NOT NULL DEFAULT 0,
    missing_count   int         NOT NULL DEFAULT 0,
    warnings        jsonb       NOT NULL DEFAULT '[]',
    report_hash     text        NOT NULL DEFAULT '',
    error           text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    applied_at      timestamptz,
    canceled_at     timestamptz
);
CREATE INDEX imports_file_sha_idx ON imports (file_sha256);

-- строки отчета. хранятся целиком, чтобы применение шло ровно по тому отчету
-- который человек видел, а не по пересчитанному заново
CREATE TABLE import_items (
    import_id    bigint  NOT NULL REFERENCES imports (id) ON DELETE CASCADE,
    seq          int     NOT NULL,
    line         int     NOT NULL,
    kind         text    NOT NULL CHECK (kind IN ('new', 'changed', 'unchanged', 'invalid', 'missing')),
    sku          text    NOT NULL,
    base_version bigint  NOT NULL DEFAULT 0,
    old_state    jsonb,
    new_state    jsonb,
    changes      jsonb   NOT NULL DEFAULT '[]',
    errors       jsonb   NOT NULL DEFAULT '[]',
    warnings     jsonb   NOT NULL DEFAULT '[]',
    raw          jsonb   NOT NULL DEFAULT '[]',
    PRIMARY KEY (import_id, seq)
);
CREATE INDEX import_items_kind_idx ON import_items (import_id, kind, seq);
