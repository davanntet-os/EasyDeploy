<!--
Thanks for contributing to EasyDeploy! Please fill this in so reviewers have
the context they need. Keep the PR focused on one change.
-->

## Summary

<!-- What does this change do, and why? -->

## Related issue

<!-- e.g. Closes #123 -->

## Type of change

- [ ] Bug fix
- [ ] New feature
- [ ] Refactor / cleanup
- [ ] Documentation
- [ ] Other:

## How was this tested?

<!--
There is no broad automated test suite yet, so please describe how you verified
the change — the exact commands or steps, and what you observed.
-->

## Checklist

- [ ] `make vet` and `cd server && go build ./...` pass
- [ ] `cd web && npm run build` passes (no type errors)
- [ ] I manually verified the behavior I changed
- [ ] Any DB schema change is an **additive** migration (`ADD COLUMN IF NOT EXISTS` / `CREATE TABLE IF NOT EXISTS`)
- [ ] I did **not** bump the pinned Docker SDK (`docker v27.3.1+incompatible`)
- [ ] Docs/README/CLAUDE.md updated if behavior or setup changed
