# AWS portability sandbox

This tree is intentionally separate from GCP. It creates private EKS worker
subnets, an EKS control plane, an on-demand node group, an Elastic IP-backed NAT
Gateway, immutable ECR, Secrets Manager object containers, short-retention
control-plane logs, and a budget.

The `allow_paid_eks` validation is an entry gate. Confirm that the selected AWS
account plan permits EKS, NAT Gateway, NLB, and the required compute before
setting it to `true`. Destroy the GCP environment first; do not run both
learning clusters concurrently.

Create a versioned S3 state bucket and lock table first, then initialize with
`terraform init -backend-config="bucket=..." -backend-config="key=seev/plan-63.tfstate" -backend-config="region=..." -backend-config="dynamodb_table=..."`.

The Traefik Service must use an AWS-only overlay after the cluster exists. Use a
Network Load Balancer deliberately, test whether instance or IP target mode
preserves the peer address, and keep the Kubernetes application chart
unchanged. The Elastic IP output is the expected outbound source address.

No secret values are managed here. Populate the named Secrets Manager objects
through an approved secret workflow and connect them to Kubernetes later via
External Secrets or the AWS provider integration.
