variable "region" { type = string }
variable "name" { type = string }
variable "vpc_id" { type = string }
variable "private_subnet_ids" { type = list(string) }
variable "private_cidr_blocks" { type = list(string) }
variable "enabled" {
  type        = bool
  description = "Opt-in because these managed services incur cloud charges."
  default     = false
}
variable "postgres_database" {
  type    = string
  default = "seev"
}
variable "postgres_username" {
  type    = string
  default = "seev_app"
}
variable "postgres_password" {
  type      = string
  sensitive = true
  default   = null
}
variable "postgres_instance_class" {
  type    = string
  default = "db.t4g.small"
}
variable "redis_node_type" {
  type    = string
  default = "cache.t4g.small"
}
variable "redis_auth_token" {
  type      = string
  sensitive = true
  default   = null
}
variable "mq_username" {
  type    = string
  default = "seev_app"
}
variable "mq_password" {
  type      = string
  sensitive = true
  default   = null
}
variable "tags" {
  type    = map(string)
  default = {}
}

variable "require_explicit_passwords" {
  type        = bool
  description = "Production guard: do not create managed data services without externally generated credentials."
  default     = true
}
