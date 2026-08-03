variable "region" {
  type    = string
  default = "ap-southeast-3"
}

variable "name" {
  type    = string
  default = "seev-learning"
}

variable "vpc_cidr" {
  type    = string
  default = "10.40.0.0/16"
}

variable "cluster_version" {
  description = "Pin after checking EKS support and the selected account plan."
  type        = string
  default     = "1.31"
}

variable "allow_paid_eks" {
  description = "Explicit acknowledgement that EKS/NAT/NLB/compute may be billable."
  type        = bool
  default     = false
  validation {
    condition     = var.allow_paid_eks
    error_message = "Set allow_paid_eks=true only after confirming the AWS account plan and budget."
  }
}

variable "budget_limit_usd" {
  type    = number
  default = 25
}

variable "tags" {
  type = map(string)
  default = {
    environment = "learning"
    owner       = "seev"
    managed_by  = "terraform"
  }
}
