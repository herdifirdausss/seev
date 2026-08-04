locals {
  count = var.enabled ? 1 : 0
}

resource "aws_kms_key" "platform" {
  count                   = local.count
  description             = "${var.name} data and object encryption"
  deletion_window_in_days = 30
  enable_key_rotation     = true

  lifecycle {
    precondition {
      condition     = !var.require_explicit_passwords || (var.postgres_password != null && var.redis_auth_token != null && var.mq_password != null)
      error_message = "Set explicit Postgres, Redis, and broker credentials before enabling the AWS platform module."
    }
  }
}

resource "aws_kms_alias" "platform" {
  count         = local.count
  name          = "alias/${var.name}-platform"
  target_key_id = aws_kms_key.platform[0].key_id
}

resource "aws_security_group" "data" {
  count       = local.count
  name        = "${var.name}-managed-data"
  description = "Private managed Seev data services"
  vpc_id      = var.vpc_id

  dynamic "ingress" {
    for_each = toset([5432, 6379, 5671])
    content {
      description = "private application network"
      from_port   = ingress.value
      to_port     = ingress.value
      protocol    = "tcp"
      cidr_blocks = var.private_cidr_blocks
    }
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_db_subnet_group" "postgres" {
  count      = local.count
  name       = "${var.name}-postgres"
  subnet_ids = var.private_subnet_ids
}

resource "aws_db_instance" "postgres" {
  count                   = local.count
  identifier              = "${var.name}-postgres"
  engine                  = "postgres"
  engine_version          = "16"
  instance_class          = var.postgres_instance_class
  allocated_storage       = 50
  max_allocated_storage   = 200
  storage_type            = "gp3"
  storage_encrypted       = true
  kms_key_id              = aws_kms_key.platform[0].arn
  db_name                 = var.postgres_database
  username                = var.postgres_username
  password                = var.postgres_password
  port                    = 5432
  multi_az                = true
  publicly_accessible     = false
  backup_retention_period = 7
  deletion_protection     = true
  skip_final_snapshot     = false
  db_subnet_group_name    = aws_db_subnet_group.postgres[0].name
  vpc_security_group_ids  = [aws_security_group.data[0].id]
}

resource "aws_elasticache_subnet_group" "redis" {
  count      = local.count
  name       = "${var.name}-redis"
  subnet_ids = var.private_subnet_ids
}

resource "aws_elasticache_replication_group" "redis" {
  count                      = local.count
  replication_group_id       = "${var.name}-redis"
  description                = "${var.name} private Redis"
  engine                     = "redis"
  engine_version             = "7.1"
  node_type                  = var.redis_node_type
  num_cache_clusters         = 2
  automatic_failover_enabled = true
  multi_az_enabled           = true
  at_rest_encryption_enabled = true
  transit_encryption_enabled = true
  auth_token                 = var.redis_auth_token
  kms_key_id                 = aws_kms_key.platform[0].arn
  port                       = 6379
  subnet_group_name          = aws_elasticache_subnet_group.redis[0].name
  security_group_ids         = [aws_security_group.data[0].id]
}

resource "aws_mq_broker" "rabbitmq" {
  count                      = local.count
  broker_name                = "${var.name}-rabbitmq"
  engine_type                = "RABBITMQ"
  engine_version             = "3.13"
  host_instance_type         = "mq.t3.micro"
  deployment_mode            = "ACTIVE_STANDBY_MULTI_AZ"
  publicly_accessible        = false
  subnet_ids                 = slice(var.private_subnet_ids, 0, 2)
  security_groups            = [aws_security_group.data[0].id]
  auto_minor_version_upgrade = true

  user {
    username = var.mq_username
    password = var.mq_password
  }

  logs { general = true }
}

resource "aws_s3_bucket" "objects" {
  count  = local.count
  bucket = "${var.name}-objects"
}

resource "aws_s3_bucket_public_access_block" "objects" {
  count                   = local.count
  bucket                  = aws_s3_bucket.objects[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "objects" {
  count  = local.count
  bucket = aws_s3_bucket.objects[0].id
  rule {
    apply_server_side_encryption_by_default {
      kms_master_key_id = aws_kms_key.platform[0].arn
      sse_algorithm     = "aws:kms"
    }
  }
}

resource "aws_s3_bucket_versioning" "objects" {
  count  = local.count
  bucket = aws_s3_bucket.objects[0].id
  versioning_configuration { status = "Enabled" }
}

resource "aws_cloudwatch_log_group" "platform" {
  count             = local.count
  name              = "/seev/${var.name}/platform"
  retention_in_days = 30
  kms_key_id        = aws_kms_key.platform[0].arn
}

resource "aws_secretsmanager_secret" "runtime" {
  for_each   = var.enabled ? toset(["postgres", "redis", "rabbitmq", "application"]) : toset([])
  name       = "${var.name}/${each.key}"
  kms_key_id = aws_kms_key.platform[0].arn
}
