variable "project_id" { type = string }
variable "region" { type = string }
variable "name" { type = string }
variable "private_network" { type = string }
variable "authorized_network" { type = string }
variable "enabled" {
  type        = bool
  description = "Opt-in because these managed services incur cloud charges."
  default     = false
}
variable "postgres_tier" {
  type    = string
  default = "db-custom-2-7680"
}
variable "postgres_password" {
  type      = string
  sensitive = true
  default   = null
}
variable "redis_memory_size_gb" {
  type    = number
  default = 5
}
variable "require_explicit_password" {
  type    = bool
  default = true
}
