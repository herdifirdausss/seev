output "cluster_name" {
  value = google_container_cluster.gke.name
}

output "cluster_location" {
  value = google_container_cluster.gke.location
}

output "artifact_registry_repository" {
  value = google_artifact_registry_repository.images.name
}

output "reserved_ingress_ip" {
  value = google_compute_address.ingress.address
}

output "reserved_egress_ip" {
  value = google_compute_address.egress.address
}

output "workload_service_account" {
  value = google_service_account.workload.email
}
