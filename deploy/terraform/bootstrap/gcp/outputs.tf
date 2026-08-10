output "state_bucket_name" {
  description = "GCS bucket name for Terraform remote state — use in backend.hcl for dev/platform modules"
  value       = google_storage_bucket.tfstate.name
}

output "workload_identity_provider" {
  description = "Workload Identity Provider resource name — use as workload_identity_provider in GitHub Actions"
  value       = google_iam_workload_identity_pool_provider.github.name
}

output "ci_deploy_service_account" {
  description = "CI deploy service account email — use as service_account in GitHub Actions"
  value       = google_service_account.ci_deploy.email
}
