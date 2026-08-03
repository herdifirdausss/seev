locals {
  prefix = var.name
  labels = merge(var.labels, { plan = "63", provider = "gcp" })
}

resource "google_project_service" "apis" {
  for_each = toset([
    "artifactregistry.googleapis.com",
    "billingbudgets.googleapis.com",
    "cloudbilling.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "compute.googleapis.com",
    "container.googleapis.com",
    "dns.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "logging.googleapis.com",
    "monitoring.googleapis.com",
    "secretmanager.googleapis.com",
  ])
  project            = var.project_id
  service            = each.key
  disable_on_destroy = false
}

resource "google_compute_network" "vpc" {
  name                    = "${local.prefix}-vpc"
  auto_create_subnetworks = false
  description             = "Plan 63 private GKE learning network"
  routing_mode            = "REGIONAL"
}

resource "google_compute_subnetwork" "gke" {
  name                     = "${local.prefix}-gke"
  region                   = var.region
  network                  = google_compute_network.vpc.id
  ip_cidr_range            = "10.20.0.0/20"
  private_ip_google_access = true

  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = "10.24.0.0/14"
  }
  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = "10.28.0.0/20"
  }
}

resource "google_compute_router" "nat" {
  name    = "${local.prefix}-router"
  region  = var.region
  network = google_compute_network.vpc.id
}

resource "google_compute_address" "egress" {
  name         = "${local.prefix}-egress-ip"
  region       = var.region
  address_type = "EXTERNAL"
  labels       = local.labels
}

resource "google_compute_router_nat" "nat" {
  name                               = "${local.prefix}-nat"
  router                             = google_compute_router.nat.name
  region                             = var.region
  nat_ip_allocate_option             = "MANUAL_ONLY"
  nat_ips                            = [google_compute_address.egress.self_link]
  source_subnetwork_ip_ranges_to_nat = "LIST_OF_SUBNETWORKS"
  min_ports_per_vm                   = 128

  subnetwork {
    name                    = google_compute_subnetwork.gke.self_link
    source_ip_ranges_to_nat = ["ALL_IP_RANGES"]
  }
  log_config {
    enable = true
    filter = "ERRORS_ONLY"
  }
}

resource "google_compute_address" "ingress" {
  name         = "${local.prefix}-ingress-ip"
  region       = var.region
  address_type = "EXTERNAL"
  labels       = local.labels
}

resource "google_container_cluster" "gke" {
  name                     = "${local.prefix}-gke"
  location                 = var.zone
  project                  = var.project_id
  min_master_version       = var.cluster_version
  remove_default_node_pool = true
  initial_node_count       = 1
  networking_mode          = "VPC_NATIVE"
  network                  = google_compute_network.vpc.name
  subnetwork               = google_compute_subnetwork.gke.name
  datapath_provider        = "ADVANCED_DATAPATH"
  deletion_protection      = false

  release_channel {
    channel = "REGULAR"
  }
  ip_allocation_policy {
    cluster_secondary_range_name  = "pods"
    services_secondary_range_name = "services"
  }
  private_cluster_config {
    enable_private_nodes    = true
    enable_private_endpoint = false
    master_ipv4_cidr_block  = "172.16.0.0/28"
  }
  master_authorized_networks_config {
    dynamic "cidr_blocks" {
      for_each = var.authorized_operator_cidrs
      content {
        cidr_block   = cidr_blocks.value
        display_name = "approved-operator"
      }
    }
  }
  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }
  gateway_api_config {
    channel = "CHANNEL_STANDARD"
  }
  resource_labels = local.labels
  depends_on      = [google_project_service.apis]
}

resource "google_container_node_pool" "on_demand" {
  name       = "on-demand"
  location   = var.zone
  cluster    = google_container_cluster.gke.name
  node_count = 1

  autoscaling {
    min_node_count = 1
    max_node_count = 2
  }
  management {
    auto_repair  = true
    auto_upgrade = true
  }
  node_config {
    machine_type    = "e2-standard-2"
    disk_type       = "pd-balanced"
    disk_size_gb    = 50
    image_type      = "COS_CONTAINERD"
    service_account = google_service_account.nodes.email
    oauth_scopes    = ["https://www.googleapis.com/auth/cloud-platform"]
    labels          = local.labels
    tags            = [local.prefix]
    workload_metadata_config {
      mode = "GKE_METADATA"
    }
  }
}

resource "google_service_account" "nodes" {
  account_id   = "${local.prefix}-nodes"
  display_name = "SeeV learning GKE nodes"
}

resource "google_project_iam_member" "nodes_logging" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.nodes.email}"
}

resource "google_project_iam_member" "nodes_monitoring" {
  project = var.project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.nodes.email}"
}

resource "google_project_iam_member" "nodes_monitoring_viewer" {
  project = var.project_id
  role    = "roles/monitoring.viewer"
  member  = "serviceAccount:${google_service_account.nodes.email}"
}

resource "google_project_iam_member" "nodes_artifact_reader" {
  project = var.project_id
  role    = "roles/artifactregistry.reader"
  member  = "serviceAccount:${google_service_account.nodes.email}"
}

resource "google_artifact_registry_repository" "images" {
  location      = var.region
  repository_id = "${local.prefix}-images"
  description   = "Immutable Seev learning images"
  format        = "DOCKER"
  labels        = local.labels
  depends_on    = [google_project_service.apis]
}

resource "google_secret_manager_secret" "runtime" {
  for_each  = toset(["seev-runtime", "seev-data", "seev-crypto", "seev-mtls"])
  secret_id = "${local.prefix}-${each.key}"
  replication {
    auto {}
  }
  labels = local.labels
}

resource "google_service_account" "workload" {
  account_id   = "${local.prefix}-vendor"
  display_name = "SeeV VendorService workload identity"
}

resource "google_service_account_iam_member" "workload_identity" {
  service_account_id = google_service_account.workload.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[seev-app/seev-vendor]"
}

resource "google_billing_budget" "learning" {
  billing_account = var.billing_account
  display_name    = "${local.prefix} monthly guardrail"
  amount {
    specified_amount {
      currency_code = "USD"
      units         = tostring(var.budget_amount_usd)
    }
  }
  threshold_rules { threshold_percent = 0.5 }
  threshold_rules { threshold_percent = 0.8 }
  threshold_rules { threshold_percent = 1.0 }
  budget_filter {
    projects = ["projects/${data.google_project.current.number}"]
  }
}

data "google_project" "current" {
  project_id = var.project_id
}

resource "google_dns_managed_zone" "dev" {
  count       = var.dns_name == "" ? 0 : 1
  name        = replace(var.name, "-", "-")
  dns_name    = var.dns_name
  description = "SeeV learning DNS zone"
  labels      = local.labels
}

resource "google_dns_record_set" "api" {
  count        = var.dns_name == "" ? 0 : 1
  managed_zone = google_dns_managed_zone.dev[0].name
  name         = "api.${var.dns_name}"
  type         = "A"
  ttl          = 60
  rrdatas      = [google_compute_address.ingress.address]
}

resource "google_dns_record_set" "callback" {
  count        = var.dns_name == "" ? 0 : 1
  managed_zone = google_dns_managed_zone.dev[0].name
  name         = "callback.${var.dns_name}"
  type         = "A"
  ttl          = 60
  rrdatas      = [google_compute_address.ingress.address]
}
