data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  prefix = var.name
  azs    = slice(data.aws_availability_zones.available.names, 0, 2)
  public_subnets = {
    for index, az in local.azs : az => cidrsubnet(var.vpc_cidr, 4, index)
  }
  private_subnets = {
    for index, az in local.azs : az => cidrsubnet(var.vpc_cidr, 4, index + 8)
  }
}

resource "aws_vpc" "main" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
}

resource "aws_subnet" "public" {
  for_each                = local.public_subnets
  vpc_id                  = aws_vpc.main.id
  availability_zone       = each.key
  cidr_block              = each.value
  map_public_ip_on_launch = true
}

resource "aws_subnet" "private" {
  for_each          = local.private_subnets
  vpc_id            = aws_vpc.main.id
  availability_zone = each.key
  cidr_block        = each.value
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }
}

resource "aws_route_table_association" "public" {
  for_each       = aws_subnet.public
  subnet_id      = each.value.id
  route_table_id = aws_route_table.public.id
}

resource "aws_eip" "nat" {
  domain = "vpc"
}

resource "aws_nat_gateway" "main" {
  allocation_id = aws_eip.nat.id
  subnet_id     = values(aws_subnet.public)[0].id
  depends_on    = [aws_internet_gateway.main]
}

resource "aws_route_table" "private" {
  for_each = aws_subnet.private
  vpc_id   = aws_vpc.main.id
  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.main.id
  }
}

resource "aws_route_table_association" "private" {
  for_each       = aws_subnet.private
  subnet_id      = each.value.id
  route_table_id = aws_route_table.private[each.key].id
}

resource "aws_iam_role" "cluster" {
  name = "${local.prefix}-eks-cluster"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "eks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "cluster_policy" {
  role       = aws_iam_role.cluster.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"
}

resource "aws_eks_cluster" "main" {
  name     = local.prefix
  role_arn = aws_iam_role.cluster.arn
  version  = var.cluster_version
  vpc_config {
    subnet_ids              = [for subnet in aws_subnet.private : subnet.id]
    endpoint_private_access = true
    endpoint_public_access  = true
  }
  enabled_cluster_log_types = ["api", "audit", "authenticator"]
  depends_on                = [aws_iam_role_policy_attachment.cluster_policy]
}

data "tls_certificate" "eks_oidc" {
  url = aws_eks_cluster.main.identity[0].oidc[0].issuer
}

resource "aws_iam_openid_connect_provider" "eks" {
  url             = aws_eks_cluster.main.identity[0].oidc[0].issuer
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.eks_oidc.certificates[0].sha1_fingerprint]
}

resource "aws_iam_role" "vendor_workload" {
  name = "${local.prefix}-vendor-workload"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = aws_iam_openid_connect_provider.eks.arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "${replace(aws_eks_cluster.main.identity[0].oidc[0].issuer, "https://", "")}:aud" = "sts.amazonaws.com"
          "${replace(aws_eks_cluster.main.identity[0].oidc[0].issuer, "https://", "")}:sub" = "system:serviceaccount:seev-app:seev-vendor"
        }
      }
    }]
  })
}

resource "aws_iam_role" "nodes" {
  name = "${local.prefix}-eks-nodes"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "node_policies" {
  for_each = toset([
    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
    "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
  ])
  role       = aws_iam_role.nodes.name
  policy_arn = each.key
}

resource "aws_eks_node_group" "on_demand" {
  cluster_name    = aws_eks_cluster.main.name
  node_group_name = "on-demand"
  node_role_arn   = aws_iam_role.nodes.arn
  subnet_ids      = [for subnet in aws_subnet.private : subnet.id]
  instance_types  = ["t3.large"]
  scaling_config {
    desired_size = 1
    min_size     = 1
    max_size     = 2
  }
  update_config { max_unavailable = 1 }
  depends_on = [aws_iam_role_policy_attachment.node_policies]
}

resource "aws_ecr_repository" "images" {
  name                 = local.prefix
  image_tag_mutability = "IMMUTABLE"
  image_scanning_configuration { scan_on_push = true }
}

resource "aws_ecr_repository" "migrations" {
  name                 = "${local.prefix}/migrations"
  image_tag_mutability = "IMMUTABLE"
  image_scanning_configuration { scan_on_push = true }
}

resource "aws_secretsmanager_secret" "runtime" {
  for_each = toset(["runtime", "data", "crypto", "mtls"])
  name     = "${local.prefix}/${each.key}"
}

resource "aws_iam_role_policy" "vendor_secret_reader" {
  name = "${local.prefix}-vendor-secret-reader"
  role = aws_iam_role.vendor_workload.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["secretsmanager:DescribeSecret", "secretsmanager:GetSecretValue"]
      Resource = [for secret in aws_secretsmanager_secret.runtime : secret.arn]
    }]
  })
}

resource "aws_cloudwatch_log_group" "eks" {
  name              = "/aws/eks/${local.prefix}/cluster"
  retention_in_days = 7
}

resource "aws_budgets_budget" "learning" {
  name         = "${local.prefix}-monthly"
  budget_type  = "COST"
  limit_amount = tostring(var.budget_limit_usd)
  limit_unit   = "USD"
  time_unit    = "MONTHLY"
  cost_filter {
    name   = "TagKeyValue"
    values = ["user:environment$learning"]
  }
}
