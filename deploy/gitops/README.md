# GitOps handoff

GitOps begins only after the manual local/cloud deployment is understood. The
Argo CD Applications below intentionally point at the checked-in Helm chart
and provider values; image tags must be immutable digests before a cloud app is
enabled.

Stage 1 is manual Helm plus evidence. Stage 2 installs the local Application
and observes drift. Stage 3 promotes the same chart through provider-specific
values after smoke/E2E gates. Argo CD is never given a secret value in Git.
