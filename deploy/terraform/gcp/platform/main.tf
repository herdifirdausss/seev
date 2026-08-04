locals { count = var.enabled ? 1 : 0 }

resource "google_project_service" "apis" {
  for_each = var.enabled ? toset([
    "sqladmin.googleapis.com",
    "redis.googleapis.com",
    "pubsub.googleapis.com",
    "storage.googleapis.com",
    "cloudkms.googleapis.com",
    "secretmanager.googleapis.com",
  ]) : toset([])
  project            = var.project_id
  service            = each.key
  disable_on_destroy = false
}

resource "google_kms_key_ring" "platform" {
  count      = local.count
  name       = "${var.name}-platform"
  location   = var.region
  project    = var.project_id
  depends_on = [google_project_service.apis]

  lifecycle {
    precondition {
      condition     = !var.require_explicit_password || var.postgres_password != null
      error_message = "Set an externally generated Postgres password before enabling the GCP platform module."
    }
  }
}

resource "google_kms_crypto_key" "objects" {
  count           = local.count
  name            = "objects"
  key_ring        = google_kms_key_ring.platform[0].id
  rotation_period = "7776000s"
}

resource "google_sql_database_instance" "postgres" {
  count               = local.count
  name                = "${var.name}-postgres"
  database_version    = "POSTGRES_16"
  region              = var.region
  deletion_protection = true

  settings {
    tier              = var.postgres_tier
    availability_type = "REGIONAL"
    disk_type         = "PD_SSD"
    disk_size         = 50
    disk_autoresize   = true
    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
      transaction_log_retention_days = 7
    }
    ip_configuration {
      ipv4_enabled    = false
      private_network = var.private_network
      ssl_mode        = "ENCRYPTED_ONLY"
    }
    database_flags {
      name  = "log_checkpoints"
      value = "on"
    }
  }
  depends_on = [google_project_service.apis]
}

resource "google_sql_database" "application" {
  count    = local.count
  name     = "seev"
  instance = google_sql_database_instance.postgres[0].name
}

resource "google_sql_user" "application" {
  count    = local.count
  name     = "seev_app"
  instance = google_sql_database_instance.postgres[0].name
  password = var.postgres_password
}

resource "google_redis_instance" "redis" {
  count                   = local.count
  name                    = "${var.name}-redis"
  tier                    = "STANDARD_HA"
  memory_size_gb          = var.redis_memory_size_gb
  region                  = var.region
  authorized_network      = var.authorized_network
  connect_mode            = "PRIVATE_SERVICE_ACCESS"
  redis_version           = "REDIS_7_2"
  auth_enabled            = true
  transit_encryption_mode = "SERVER_AUTHENTICATION"
  depends_on              = [google_project_service.apis]
}

resource "google_pubsub_topic" "events" {
  count   = local.count
  name    = "${var.name}-events"
  project = var.project_id
}

resource "google_pubsub_subscription" "events" {
  count                      = local.count
  name                       = "${var.name}-events-consumer"
  topic                      = google_pubsub_topic.events[0].name
  ack_deadline_seconds       = 30
  retain_acked_messages      = true
  message_retention_duration = "604800s"
}

resource "google_storage_bucket" "objects" {
  count                       = local.count
  name                        = "${var.name}-objects-${var.project_id}"
  location                    = var.region
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  versioning { enabled = true }
  encryption { default_kms_key_name = google_kms_crypto_key.objects[0].id }
  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret" "runtime" {
  for_each  = var.enabled ? toset(["postgres", "redis", "rabbitmq", "application"]) : toset([])
  secret_id = "${var.name}-${each.key}"
  replication {
    auto {}
  }
  depends_on = [google_project_service.apis]
}
