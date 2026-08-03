output "cluster_name" {
  value = aws_eks_cluster.main.name
}

output "nat_elastic_ip" {
  value = aws_eip.nat.public_ip
}

output "ecr_repository_url" {
  value = aws_ecr_repository.images.repository_url
}

output "ecr_migrations_repository_url" {
  value = aws_ecr_repository.migrations.repository_url
}

output "private_subnet_ids" {
  value = [for subnet in aws_subnet.private : subnet.id]
}

output "vendor_workload_role_arn" {
  value = aws_iam_role.vendor_workload.arn
}
