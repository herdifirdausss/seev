output "state_bucket_name" {
  description = "S3 bucket name for Terraform remote state — use in backend.hcl for dev/platform modules"
  value       = aws_s3_bucket.tfstate.bucket
}

output "lock_table_name" {
  description = "DynamoDB table name for Terraform state locking"
  value       = aws_dynamodb_table.tflock.name
}

output "ci_deploy_role_arn" {
  description = "IAM role ARN for CI deployments — configure as OIDC role in GitHub Actions"
  value       = aws_iam_role.ci_deploy.arn
}
