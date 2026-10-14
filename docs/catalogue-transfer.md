# Catalogue import and export

Billmesh catalogue transfers are portable administrative documents. They contain
product policy, entitlement schemas, plans, and credit packs, but never contain
database IDs, row versions, audit timestamps, payment-provider credentials, or
other secrets.

## Versioning

The current export format is schema version 2. The importer also accepts version
1 documents and treats their missing `credit_packs` collections as empty.
Exports always use version 2.

## Administrative operations

- `GET /v1/admin/catalogue/export` reads the complete catalogue in a repeatable
  read transaction and returns a lossless version 2 document.
- `POST /v1/admin/catalogue/import/validate` validates a document and reports
  issues using JSON Pointer paths. It makes no changes.
- `POST /v1/admin/catalogue/import` performs the same validation and then creates
  every product, plan, and credit pack in one database transaction.

Import is intentionally create-only. If any product slug already exists, active
or archived, the entire import is rejected. Billmesh never silently overwrites
catalogue history. Any validation error or database failure rolls back the whole
operation.

## Bulk create and update

Versioned bulk change sets are separate from portable create-only transfers:

- `GET /v1/admin/catalogue/bulk/template` returns an empty schema-version 1
  change set.
- `POST /v1/admin/catalogue/bulk/validate` performs the same database-aware
  work as apply, then rolls the transaction back.
- `POST /v1/admin/catalogue/bulk/apply` commits the complete change set in one
  transaction.

Every product, plan, and credit pack declares `operation` as `create` or
`update`. Creates require `expected_version: 0`; updates require the current
positive row version. Delegated-access entries use the same rule. A stale
version, missing update target, existing create target, validation error, or
database failure rejects the whole change set. Omitted nested resources are
left unchanged; bulk apply does not imply deletion.

Product create/update documents may include `delegated_access`. Each entry
contains one environment, one IdNest grant-authorizer client ID, and the actor
bindings required by that product implementation. Each actor selects a
Billmesh-controlled profile (`catalogue`, `billing`, or `runtime`) and an
allowed actor type; Billmesh derives the scope and atomically materializes the
runtime profiles. The actors array fully replaces that environment's previous
actor set. Omitting `delegated_access` leaves existing configuration unchanged;
including it updates only the listed environments. Archiving a product disables
every materialized profile.

For example, a product that needs only catalogue reads and runtime reporting
can use this fragment; it does not need billing actors:

```json
{
  "delegated_access": [
    {
      "environment": "development",
      "expected_version": 0,
      "enabled": true,
      "authorizer_client_id": "example-billmesh-authorizer-dev",
      "actors": [
        {
          "client_id": "example-billmesh-catalogue-dev",
          "profile": "catalogue",
          "actor_type": "service",
          "enabled": true
        },
        {
          "client_id": "example-billmesh-runtime-dev",
          "profile": "runtime",
          "actor_type": "service",
          "enabled": true
        }
      ]
    }
  ]
}
```

## Environment-specific policy

`billing_policy.checkout.allowed_redirect_origins` is configuration, not a
secret, and is preserved by export. Use a separate reviewed catalogue document
per environment when development, staging, and production origins differ.
Origins must use HTTPS, except for `http://localhost` and
`http://127.0.0.1` during local development.

An empty origin list is valid. It requires consumers to omit `success_url` and
`cancel_url`, which is suitable for modal checkout flows that confirm payment by
polling and provider webhooks.

## Safe workflow

1. Validate the transfer.
2. Review every returned issue and the product, plan, and credit-pack counts.
3. Import only after validation succeeds.
4. Fetch the customer catalogue and run the consuming application's staging
   acceptance journey.
5. Export the resulting catalogue and retain the reviewed document as the
   portable configuration record.

The production candidate for Daybook is
`deploy/catalogues/daybook-catalogue.v2.json`.
