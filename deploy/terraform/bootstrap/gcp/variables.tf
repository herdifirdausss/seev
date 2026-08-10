variable "gcp_project" {
  description = "GCP project ID"
  type        = string
}

variable "gcp_region" {
  description = "GCP region for bootstrap resources"
  type        = string
  default     = "asia-southeast1"
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
  description = "GCS bucket name for Terraform remote state"
  type        = string
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
