terraform {
  required_version = ">= 1.8.0, < 2.0.0"
  backend "s3" {}
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }
}

provider "aws" {
  region = var.region
  default_tags {
    tags = merge(var.tags, { plan = "63", provider = "aws" })
  }
}
