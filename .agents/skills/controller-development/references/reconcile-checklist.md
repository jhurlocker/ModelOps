# Reconcile Checklist

- [ ] Fetch parent resource.
- [ ] Ignore NotFound.
- [ ] Initialize or validate status.
- [ ] Resolve the lifecycle profile.
- [ ] Resolve platform configuration.
- [ ] Validate credential references.
- [ ] Inspect expected child resources.
- [ ] Create only the next missing or eligible child.
- [ ] Observe child state.
- [ ] Update conditions using `observedGeneration`.
- [ ] Return a bounded requeue when waiting.
- [ ] Avoid status writes when nothing changed.

For sequential stages:

```text
missing → create → return
running → wait → return
failed  → mark failed → return
succeeded → continue
```
