# deploy/terraform/bootstrap/gcp/main.tf — DEPLOY-007
#
# One-time bootstrap layer for GCP: creates the remote state backend (GCS
# bucket + versioning) and least-privilege CI identity (Workload Identity
# Federation for GitHub Actions — no long-lived service account keys).
#
# Run this ONCE from a local terminal with temporary owner credentials before
# running `terraform init` in deploy/terraform/gcp/dev or gcp/platform.
# State for this module is stored locally (bootstrap/terraform.tfstate).

terraform {
  required_version = ">= 1.8"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
  }
}

provider "google" {
  project = var.gcp_project
  region  = var.gcp_region
}

# ---- Remote state: GCS bucket ----

resource "google_storage_bucket" "tfstate" {
  name          = var.state_bucket_name
  location      = var.gcp_region
  force_destroy = false

  versioning {
    enabled = true
  }

  uniform_bucket_level_access = true

  labels = local.labels

  lifecycle {
    prevent_destroy = true
  }
}

# ---- CI identity: Workload Identity Federation for GitHub Actions ----

resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "${var.project}-github-pool"
  display_name              = "GitHub Actions — ${var.project}"
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github-provider"
  display_name                       = "GitHub OIDC"

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.repository" = "assertion.repository"
    "attribute.ref"        = "assertion.ref"
  }

  attribute_condition = "attribute.repository == '${var.github_org}/${var.github_repo}'"
}

resource "google_service_account" "ci_deploy" {
  account_id   = "${var.project}-ci-deploy-${var.environment}"
  display_name = "Seev CI deploy — ${var.environment}"
}

resource "google_service_account_iam_binding" "wif_binding" {
  service_account_id = google_service_account.ci_deploy.name
  role               = "roles/iam.workloadIdentityUser"

  members = [
    "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository/${var.github_org}/${var.github_repo}",
  ]
}

resource "google_storage_bucket_iam_member" "ci_tfstate" {
  bucket = google_storage_bucket.tfstate.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.ci_deploy.email}"
}

# ---- Audit logging ----

resource "google_project_iam_audit_config" "infra" {
  project = var.gcp_project
  service = "allServices"

  audit_log_config {
    log_type = "ADMIN_READ"
  }
  audit_log_config {
    log_type = "DATA_WRITE"
  }
}

locals {
  labels = {
    "seev-managed-by"  = "terraform-bootstrap"
    "seev-environment" = var.environment
  }
}
