output "postgres_connection_name" { value = try(google_sql_database_instance.postgres[0].connection_name, null) }
output "postgres_private_ip" { value = try(google_sql_database_instance.postgres[0].private_ip_address, null) }
output "redis_host" { value = try(google_redis_instance.redis[0].host, null) }
output "redis_auth_string" {
  value     = try(google_redis_instance.redis[0].auth_string, null)
  sensitive = true
}
output "events_topic" { value = try(google_pubsub_topic.events[0].id, null) }
output "object_bucket" { value = try(google_storage_bucket.objects[0].name, null) }
output "kms_key" { value = try(google_kms_crypto_key.objects[0].id, null) }
output "runtime_secret_ids" { value = { for name, secret in google_secret_manager_secret.runtime : name => secret.id } }
