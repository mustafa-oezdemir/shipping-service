-- Schema ownership: this database belongs exclusively to shipping-service.
-- ecommerce-gin exchanges order/customer snapshots through authenticated APIs.
CREATE TABLE IF NOT EXISTS schema_migrations (
  version VARCHAR(64) PRIMARY KEY,
  applied_at DATETIME(3) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
