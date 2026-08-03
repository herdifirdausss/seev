variable "project_id" {
  description = "Dedicated learning project; do not point this at a production project."
  type        = string
}

variable "billing_account" {
  description = "Billing account ID used only for the budget resource."
  type        = string
  sensitive   = true
}

variable "region" {
  type    = string
  default = "asia-southeast2"
}

variable "zone" {
  type    = string
  default = "asia-southeast2-a"
}

variable "name" {
  type    = string
  default = "seev-learning"
}

variable "cluster_version" {
  description = "Pin after checking GKE support in the selected project."
  type        = string
  default     = "1.31"
}

variable "authorized_operator_cidrs" {
  description = "CIDRs allowed to reach the initial public control-plane endpoint."
  type        = list(string)
  default     = []
}

variable "dns_name" {
  description = "Optional Cloud DNS zone name, including trailing dot; empty disables DNS resources."
  type        = string
  default     = ""
}

variable "budget_amount_usd" {
  type    = number
  default = 25
}

variable "labels" {
  type = map(string)
  default = {
    environment = "learning"
    owner       = "seev"
    managed_by  = "terraform"
  }
}
