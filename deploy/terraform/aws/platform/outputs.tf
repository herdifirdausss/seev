output "postgres_endpoint" { value = try(aws_db_instance.postgres[0].address, null) }
output "postgres_port" { value = try(aws_db_instance.postgres[0].port, null) }
output "redis_primary_endpoint" { value = try(aws_elasticache_replication_group.redis[0].primary_endpoint_address, null) }
output "rabbitmq_endpoints" { value = try(aws_mq_broker.rabbitmq[0].instances[*].console_url, []) }
output "object_bucket" { value = try(aws_s3_bucket.objects[0].bucket, null) }
output "kms_key_arn" { value = try(aws_kms_key.platform[0].arn, null) }
output "runtime_secret_arns" { value = { for name, secret in aws_secretsmanager_secret.runtime : name => secret.arn } }
