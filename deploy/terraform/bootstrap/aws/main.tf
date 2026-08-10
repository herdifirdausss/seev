# deploy/terraform/bootstrap/aws/main.tf — DEPLOY-007
#
# One-time bootstrap layer for AWS: creates the remote state backend (S3 +
# DynamoDB lock table) and the least-privilege CI deployment identity
# (OIDC-based IAM role for GitHub Actions — no long-lived static keys).
#
# Run this ONCE from a local terminal with temporary admin credentials before
# running `terraform init` in deploy/terraform/aws/dev or aws/platform.
# After bootstrap, CI uses the OIDC role; humans use break-glass MFA role.
#
# State for this module is stored locally (bootstrap/terraform.tfstate) and
# should be committed or stored in a secure location — it is the root of
# trust for all other state backends.

terraform {
  required_version = ">= 1.8"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

# ---- Remote state: S3 bucket ----

resource "aws_s3_bucket" "tfstate" {
  bucket = var.state_bucket_name

  lifecycle {
    prevent_destroy = true
  }

  tags = local.tags
}

resource "aws_s3_bucket_versioning" "tfstate" {
  bucket = aws_s3_bucket.tfstate.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "tfstate" {
  bucket = aws_s3_bucket.tfstate.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "aws:kms"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "tfstate" {
  bucket                  = aws_s3_bucket.tfstate.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# ---- Remote state: DynamoDB lock table ----

resource "aws_dynamodb_table" "tflock" {
  name         = var.lock_table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"

  attribute {
    name = "LockID"
    type = "S"
  }

  point_in_time_recovery {
    enabled = true
  }

  tags = local.tags
}

# ---- CI identity: OIDC provider for GitHub Actions ----

data "aws_iam_openid_connect_provider" "github" {
  count = var.create_oidc_provider ? 0 : 1
  url   = "https://token.actions.githubusercontent.com"
}

resource "aws_iam_openid_connect_provider" "github" {
  count = var.create_oidc_provider ? 1 : 0

  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1"]

  tags = local.tags
}

locals {
  oidc_provider_arn = var.create_oidc_provider ? (
    aws_iam_openid_connect_provider.github[0].arn
  ) : data.aws_iam_openid_connect_provider.github[0].arn

  tags = {
    "seev.io/managed-by"  = "terraform-bootstrap"
    "seev.io/environment" = var.environment
  }
}

# ---- CI identity: least-privilege IAM role ----

resource "aws_iam_role" "ci_deploy" {
  name = "${var.project}-ci-deploy-${var.environment}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Federated = local.oidc_provider_arn
      }
      Action = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
        }
        StringLike = {
          "token.actions.githubusercontent.com:sub" = "repo:${var.github_org}/${var.github_repo}:*"
        }
      }
    }]
  })

  tags = local.tags
}

resource "aws_iam_role_policy" "ci_deploy_state" {
  name = "tfstate-readwrite"
  role = aws_iam_role.ci_deploy.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket"]
        Resource = [aws_s3_bucket.tfstate.arn, "${aws_s3_bucket.tfstate.arn}/*"]
      },
      {
        Effect   = "Allow"
        Action   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem"]
        Resource = aws_dynamodb_table.tflock.arn
      }
    ]
  })
}

# ---- CloudTrail audit logging for infrastructure changes ----

resource "aws_cloudtrail" "infra" {
  name                          = "${var.project}-infra-audit-${var.environment}"
  s3_bucket_name                = aws_s3_bucket.tfstate.bucket
  s3_key_prefix                 = "cloudtrail"
  include_global_service_events = true
  is_multi_region_trail         = false
  enable_log_file_validation    = true

  tags = local.tags
}
