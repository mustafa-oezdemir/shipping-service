# NordShop Shipping & Returns Service

An independent Go/Gin logistics service for NordShop. It owns shipment tracking, delivery events, labels, QR codes, and operational logistics data. It does **not** read from or write to the e-commerce database.

## E-Commerce ↔ Shipping architecture

- `ecommerce-gin` remains the source of truth for orders, customers, payments, refunds, and return authorization rules.
- `shipping-service` owns shipment records, immutable sender/recipient snapshots, tracking numbers, delivery lifecycle state, timeline events, audit logs, and the ecommerce callback outbox.
- The integration is HTTP-only. No shared database tables, ORM models, or direct database imports are required or allowed.
- Shipment/return creation is idempotent via `Idempotency-Key`, shipping-side business keys, and database uniqueness constraints.
- Versioned migrations are applied through `schema_migrations`; runtime code no longer relies on `AutoMigrate`.

## Authentication and request headers

All `/api/v1/*` endpoints require `Authorization: Bearer <token>`.

### Inbound e-commerce → shipping

- `ECOMMERCE_TO_SHIPPING_TOKEN` is the active service token.
- `ECOMMERCE_TO_SHIPPING_PREVIOUS_TOKEN` is optional for rotation.
- `ECOMMERCE_SERVICE_TOKEN` is still accepted as a backward-compatible fallback env var.

### Outbound shipping → e-commerce callbacks

- `ECOMMERCE_CALLBACK_URL` is the protected e-commerce callback endpoint.
- `SHIPPING_TO_ECOMMERCE_TOKEN` is sent as `Authorization: Bearer ...` for outbox delivery (`ECOMMERCE_CALLBACK_TOKEN` remains a compatibility alias).

### Required headers

- `Idempotency-Key` for `POST /api/v1/shipments` and `POST /api/v1/returns`
- `X-Request-ID` propagated end-to-end; generated when absent
- `X-Service-Name` or `User-Agent` for source-service attribution
- `X-Shipping-Role` for privileged operational routes (`shipping_admin`, `warehouse_employee`, `delivery_employee`, `support`)

## Internal API contract

Success envelope:

```json
{
  "success": true,
  "data": {}
}
```

Error envelope:

```json
{
  "success": false,
  "error": {
    "code": "SHIPMENT_NOT_FOUND",
    "message": "Shipment not found",
    "request_id": "req_..."
  }
}
```

Focused endpoints:

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/api/v1/shipments` | Create or idempotently replay an outbound shipment |
| `GET` | `/api/v1/shipments/:trackingNumber` | JSON tracking summary without recipient PII |
| `GET` | `/api/v1/shipments/order/:orderID` | E-commerce order → shipment lookup |
| `GET` | `/api/v1/shipments/:trackingNumber/events` | Tracking timeline/events |
| `PATCH` | `/api/v1/shipments/:id/status` | Role-guarded lifecycle transition using public shipment id |
| `PATCH` | `/api/v1/shipments/:id/eta` | Role-guarded ETA update |
| `PATCH` | `/api/v1/shipments/:id/stops` | Role-guarded remaining stops update |
| `POST` | `/api/v1/shipments/:id/events` | Role-guarded timeline event append |
| `POST` | `/api/v1/returns` | Create or idempotently replay a physical return shipment |
| `GET` | `/api/v1/returns/:trackingNumber` | JSON return tracking summary |

The OpenAPI description lives in `docs/openapi.yaml`.

## Shipment lifecycle

Outbound shipments:

`created → label_created → ready_for_pickup → handed_over → received_at_origin → sorting → in_transit → arrived_at_destination_hub → out_for_delivery → delivered`

Return shipments:

`return_requested → return_authorized → return_label_created → return_in_transit → return_received → return_completed`

Each accepted status/ETA/stops/event mutation writes:

1. a shipment event
2. an audit log row
3. an outbox row for callback delivery

## Public features preserved

- `GET /track/:trackingNumber` keeps the HTML tracking page.
- `GET /qr/:trackingNumber` keeps the public QR image.
- `GET /shipments/:id/label` keeps the operational HTML label.
- Public tracking strips sender/recipient address details before rendering.

## Environment variables

| Variable | Purpose |
| --- | --- |
| `DATABASE_DSN` | Shipping database DSN |
| `APP_ENV` | Runtime environment (`development`, `test`, or `production`) |
| `APP_URL` | Canonical public tracking/QR origin; required and HTTPS-only in production |
| `GIN_MODE` | Gin mode; must be `release` in production |
| `TRUSTED_PROXIES` | Comma-separated proxy IPs/CIDRs |
| `SHIPPING_HOST_PORT` | Docker host port for Shipping (default `8090`) |
| `DATABASE_CONNECT_TIMEOUT` | Bounded startup retry window for the Shipping database |
| `ECOMMERCE_TO_SHIPPING_TOKEN` | Current inbound internal API token |
| `ECOMMERCE_TO_SHIPPING_PREVIOUS_TOKEN` | Optional previous inbound token during rotation |
| `ECOMMERCE_CALLBACK_URL` | E-commerce callback endpoint for shipping events |
| `ECOMMERCE_API_URL` | Internal E-Commerce origin; derives the callback path when an exact callback URL is omitted |
| `ECOMMERCE_PUBLIC_URL` | Browser-reachable E-Commerce origin |
| `SHIPPING_TO_ECOMMERCE_TOKEN` | Outbound callback bearer token |
| `INTERNAL_QR_SECRET` | Reserved secret for future signed internal QR flows |
| `PUBLIC_BASE_URL` | Deprecated compatibility alias for `APP_URL` |
| `OUTBOX_POLL_INTERVAL` | Dispatcher poll interval |
| `OUTBOX_RETRY_BASE_DELAY` | Base retry backoff |
| `OUTBOX_PROCESSING_STALE_AFTER` | Recover stuck processing outbox rows after this age |
| `OUTBOX_MAX_ATTEMPTS` | Max callback delivery attempts before permanent failure |
| `WAREHOUSE_*` | Sender/depot address snapshot configuration |

## Docker / local development

```bash
cp .env.example .env
docker compose config
docker compose up --build
```

- Host access: `http://localhost:8090`
- Container-to-container access: use Docker DNS such as `http://shipping-app:8090`
- Callback URLs should target the e-commerce container/service, not `localhost`, when both run in Docker

## Production deployment

The sibling `ecommerce-gin/docker-compose.production.yml` is the authoritative two-service production stack. It builds this repository, publishes Shipping at `https://pehlione-shipping.com` through Caddy, and does not expose port 8090 or the Shipping database on the host.

Production values are split by purpose:

- `APP_URL=https://pehlione-shipping.com` generates tracking links and QR contents.
- `ECOMMERCE_PUBLIC_URL=https://pehlione-ecommerce.com` identifies the browser-facing store.
- `ECOMMERCE_API_URL=http://ecommerce-app:8080` is private Docker traffic and derives the versioned callback endpoint.

Use the sibling `ecommerce-gin/scripts/New-ProductionEnv.ps1` generator to create ignored `.env.production` files for both repositories with matching, cryptographically secure service tokens. The combined stack reads its secrets from `ecommerce-gin/.env.production`; replace its remaining `CHANGE_ME` ACME/SMTP values before deployment. DNS for both domains must point to the deployment host before Caddy can obtain certificates. See the E-Commerce README for deployment and `curl` verification commands.

## Verification commands

```bash
go test ./internal/...
go build ./cmd/server
docker compose config
```
