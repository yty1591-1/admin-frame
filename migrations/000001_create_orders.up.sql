CREATE TABLE IF NOT EXISTS orders (
    id           VARCHAR(64)  PRIMARY KEY,
    merchant_id  VARCHAR(64)  NOT NULL,
    user_id      VARCHAR(64)  NOT NULL,
    amount_cent  BIGINT       NOT NULL,
    currency     VARCHAR(8)   NOT NULL DEFAULT 'CNY',
    status       VARCHAR(32)  NOT NULL,
    remark       VARCHAR(512) NOT NULL DEFAULT '',
    cost_cent    BIGINT       NOT NULL DEFAULT 0,
    version      BIGINT       NOT NULL DEFAULT 1,
    created_at   TIMESTAMP    NOT NULL,
    updated_at   TIMESTAMP    NOT NULL,
    deleted_at   TIMESTAMP    NULL
);

CREATE INDEX idx_orders_merchant_id ON orders (merchant_id);
CREATE INDEX idx_orders_user_id ON orders (user_id);
CREATE INDEX idx_orders_merchant_created ON orders (merchant_id, created_at);
