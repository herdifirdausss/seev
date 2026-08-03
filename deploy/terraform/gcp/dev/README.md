# GCP learning sandbox

This tree is intentionally provider-specific. It creates a private, zonal GKE
Standard cluster, VPC-native pod/service ranges, a manually assigned Cloud NAT
IP, a reserved regional ingress IP, Artifact Registry, Workload Identity, DNS,
Secret Manager objects, and a billing budget.

Before `apply`:

1. use a dedicated project and verify billing/credit posture;
2. create a dedicated GCS state bucket with versioning and use
   `terraform init -backend-config="bucket=..." -backend-config="prefix=seev/plan-63"`;
3. copy `terraform.tfvars.example` to an ignored tfvars file;
4. replace the operator CIDR and DNS name;
5. review `terraform plan` and the expected NAT/load-balancer cost;
6. ensure the selected GKE/Kubernetes version is supported.

Cloud NAT is configured with `MANUAL_ONLY` from its first creation. Do not
switch an already-used NAT to automatic allocation. The reserved ingress IP is
consumed by the Traefik Service overlay in
`deploy/kubernetes/platform/traefik/service-gcp-patch.yaml`.

The Secret Manager resources are object containers only. Secret values are
added by a separate approved secret bootstrap, never by Terraform variables;
this keeps credentials out of Terraform state.
