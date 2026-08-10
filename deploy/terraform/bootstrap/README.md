# Terraform Bootstrap

> DEPLOY-007: One-time foundation layer that creates remote state backends and
> least-privilege CI identities. Run once per cloud account before any other
> `terraform apply`. Never run in CI — bootstrap is a human, admin-credential
> operation.

## State Ownership

| Module | State location | Blast radius |
|---|---|---|
| `bootstrap/aws/` | local `bootstrap/terraform.tfstate` (commit or store securely) | S3 bucket, DynamoDB lock table, OIDC IAM role |
| `bootstrap/gcp/` | local `bootstrap/terraform.tfstate` (commit or store securely) | GCS bucket, Workload Identity pool+provider, CI service account |
| `aws/dev/` | S3 `seev-terraform-state/<account>/dev/terraform.tfstate` | Dev cluster, RDS, ElastiCache, networking |
| `aws/platform/` | S3 `seev-terraform-state/<account>/platform/terraform.tfstate` | Staging/prod RDS, ElastiCache, IAM |
| `gcp/dev/` | GCS `seev-terraform-state-<project>/dev/terraform.tfstate` | Dev Cloud SQL, Memorystore, networking |
| `gcp/platform/` | GCS `seev-terraform-state-<project>/platform/terraform.tfstate` | Staging/prod Cloud SQL, Memorystore |

**Rule:** bootstrap state is the root of trust. Back it up separately from workload state. A destroyed bootstrap module requires re-running from scratch with admin credentials.

## Normal CI Flow

CI (GitHub Actions) never touches bootstrap state. For workload and platform modules it authenticates via short-lived credentials:

| Provider | Mechanism | Bootstrap output |
|---|---|---|
| AWS | OIDC → `seev-ci-deploy` IAM role | `ci_role_arn` |
| GCP | Workload Identity Federation → `seev-ci@<project>.iam.gserviceaccount.com` | `ci_service_account` |

No long-lived access key or service account key is generated or stored in CI.

## Running Bootstrap (one time)

```sh
# AWS
cd deploy/terraform/bootstrap/aws
cp terraform.tfvars.example terraform.tfvars   # fill in aws_region, github_org, github_repo
terraform init                                 # local backend — no remote state yet
terraform plan
terraform apply                                # requires admin credentials (AWS_PROFILE or env vars)
# → store bootstrap/terraform.tfstate securely

# GCP
cd deploy/terraform/bootstrap/gcp
cp terraform.tfvars.example terraform.tfvars   # fill in project_id, github_org, github_repo, region
terraform init
terraform plan
terraform apply                                # requires application-default login or SA key
```

After bootstrap, configure the workload modules' backend with the outputs:
```sh
terraform output backend_bucket   # → use in aws/dev/backend.tf
terraform output ci_role_arn      # → add to GitHub Actions env vars
```

## Break-Glass Procedure

Use break-glass **only** when CI is unavailable (GitHub Actions down, OIDC misconfigured) and an urgent change must land.

1. Obtain temporary admin credentials via the organization's MFA-gated break-glass role or emergency access procedure.
2. Run from a clean checkout — verify no uncommitted changes: `git status`.
3. Apply in the narrowest-scope module possible (`aws/dev`, not `aws/platform`).
4. Create a post-incident issue documenting: timestamp, who applied, what changed, and why CI was unavailable.
5. Rotate or revoke the temporary credentials immediately after.

```sh
# Example break-glass apply for AWS dev
AWS_PROFILE=break-glass-admin terraform -chdir=deploy/terraform/aws/dev apply
```

Break-glass applies are **not** a substitute for fixing the CI pipeline. Any break-glass apply must be followed by a normal CI plan to confirm drift is zero.

## Adding a New Environment

To add a staging or production environment backed by the same bootstrap state:

1. Create `deploy/terraform/aws/staging/` (or `gcp/`) mirroring the `dev/` structure.
2. Set the backend key to a unique path (e.g. `staging/terraform.tfstate`).
3. Reference the bootstrap module's `ci_role_arn` / `ci_service_account` outputs for authentication.
4. Add the new module directory to `check-iac.sh`'s module list.
