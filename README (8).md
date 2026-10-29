# API contracts

Keep `openapi.yaml` as the source of truth for web/mobile clients and Go handlers. Generate TypeScript clients and request/response models from the OpenAPI spec in CI. Do not hand-maintain diverging client DTOs for critical workflows.

All authenticated routes must derive user identity from validated credentials, not request-body fields.
