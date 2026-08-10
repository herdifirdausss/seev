variable "aws_region" {
  description = "AWS region for bootstrap resources"
  type        = string
}

variable "project" {
  description = "Project identifier prefix (e.g. seev)"
  type        = string
  default     = "seev"
}

variable "environment" {
  description = "Target environment (dev, staging)"
  type        = string
}

variable "state_bucket_name" {
  description = "S3 bucket name for Terraform remote state"
  type        = string
}

variable "lock_table_name" {
  description = "DynamoDB table name for Terraform state locking"
  type        = string
  default     = "seev-tflock"
}

variable "github_org" {
  description = "GitHub organization owning the repository"
  type        = string
}

variable "github_repo" {
  description = "GitHub repository name"
  type        = string
  default     = "seev"
}

variable "create_oidc_provider" {
  description = "Set to true to create the GitHub OIDC provider; false if it already exists in the account"
  type        = bool
  default     = true
}
