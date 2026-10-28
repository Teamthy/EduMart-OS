# Legacy prototype — Next.js + `node:sqlite`

This directory is the **previous** implementation, kept for reference only. It is not part of any
workspace build, and nothing in `apps/` or `packages/` imports from it.

Why it was replaced:

- **Backend is now Go** (`apps/api`). Domain rules, session/deadline/score state, authz and
  persistence belong in one service, not in Next.js route handlers and server components.
- **Frontend is Next.js + Tailwind + shadcn/ui** (`apps/web`). The bespoke CSS system in
  `src/app/globals.css` was reduced to Tailwind theme tokens in `packages/shared/src/design-tokens.ts`.
- **Mobile is React Native** (`apps/mobile`), sharing the API contract in `packages/shared`.

What was mined out of it before the move: the question bank and product catalogue
(`apps/api/internal/seed/content.json`), the domain rules (now `apps/api/internal/{domain,assessment}`),
the demo accounts, and the error envelope.

Run it only if you want to compare behaviour:

```bash
cd legacy/web-sqlite && npm install && npm run dev
```
