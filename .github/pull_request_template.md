## Summary

<!-- What does this change do, and why? Keep it to a few sentences. -->

## Linked issue

<!-- Example: Closes #123. Write "None" for a small fix with no issue. -->

## Type of change

<!-- Keep the one that applies. -->

- [ ] feat: new behavior
- [ ] fix: bug fix
- [ ] refactor: no behavior change
- [ ] test: tests only
- [ ] docs: documentation only
- [ ] chore: tooling, dependencies or build

## Checklist

- [ ] Tests are added or updated, and a change in behavior has a test that fails without it
- [ ] `make lint` and `make test` pass (and `make test-admin-web` if `web/admin` changed)
- [ ] Any schema change is a new migration `migrations/NNNN_name.sql`; no shipped migration was edited
- [ ] New user-facing copy exists in English and Brazilian Portuguese (en + pt-BR)
- [ ] Documentation is updated where needed
