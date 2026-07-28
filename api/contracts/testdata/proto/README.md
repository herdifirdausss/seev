# Protobuf mutation fixtures

These text fixtures are deliberately source-level mutation cases used by the
A9 policy review and by Buf in CI:

- **add field** or RPC: allowed when additive and package-major unchanged;
- remove a field: requires its **reserved number and name**;
- **renumber** or change a field type: forbidden;
- package-major changes require a new version and concurrent rollout;
- every enum has an explicit safe **enum zero** (`*_UNSPECIFIED`), and unknown
  values must not authorize or move money;
- comparison is against the **merge base**, never an arbitrary local branch.

Run `make proto`, `make proto-lint`, and `make proto-breaking` after applying a
fixture. Generated code is checked for drift by the normal build gate.

`go test ./api/contracts` also executes `removed-field-{valid,invalid}.proto`:
the valid mutation reserves both the removed field number and name, while the
invalid mutation is rejected by the repository policy test.

The same test compares `mutation-baseline.proto` with an additive field and
with renumbered/type-changed variants. Additive fields pass; wire-number and
wire-type changes fail before a contract can be accepted.
