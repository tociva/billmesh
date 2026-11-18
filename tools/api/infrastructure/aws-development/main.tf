terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.17.0, < 7.0.0"
    }
  }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = merge({
      Application = "billmesh-api"
      Environment = "development"
      ManagedBy   = "terraform"
    }, var.tags)
  }
}

data "aws_caller_identity" "current" {}

resource "aws_iam_openid_connect_provider" "github" {
  count = var.create_github_oidc_provider ? 1 : 0

  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]

  lifecycle {
    prevent_destroy = true
  }
}

data "aws_iam_openid_connect_provider" "github" {
  count = var.create_github_oidc_provider ? 0 : 1
  url   = "https://token.actions.githubusercontent.com"

  lifecycle {
    postcondition {
      condition     = contains(self.client_id_list, "sts.amazonaws.com")
      error_message = "The existing GitHub OIDC provider must allow sts.amazonaws.com."
    }
  }
}

resource "aws_ecr_repository" "api" {
  count = var.create_ecr_repository ? 1 : 0

  name                 = var.ecr_repository_name
  image_tag_mutability = "IMMUTABLE"
  force_delete         = var.force_delete_ecr_repository

  encryption_configuration {
    encryption_type = "AES256"
  }

  image_scanning_configuration {
    scan_on_push = true
  }
}

data "aws_ecr_repository" "api" {
  count = var.create_ecr_repository ? 0 : 1
  name  = var.ecr_repository_name
}

locals {
  github_oidc_provider_arn = var.create_github_oidc_provider ? one(aws_iam_openid_connect_provider.github[*].arn) : one(data.aws_iam_openid_connect_provider.github[*].arn)
  ecr_repository_arn       = var.create_ecr_repository ? one(aws_ecr_repository.api[*].arn) : one(data.aws_ecr_repository.api[*].arn)
  audience_key             = "token.actions.githubusercontent.com:aud"
  subject_key              = "token.actions.githubusercontent.com:sub"
  repository_id_key        = "token.actions.githubusercontent.com:repository_id"
  repository_owner_id_key  = "token.actions.githubusercontent.com:repository_owner_id"
}

data "aws_iam_policy_document" "github_build_assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [local.github_oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = local.audience_key
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = local.subject_key
      values   = ["repo:${var.github_repository}:environment:${var.build_environment_name}"]
    }
    condition {
      test     = "StringEquals"
      variable = local.repository_owner_id_key
      values   = [var.github_repository_owner_id]
    }
    condition {
      test     = "StringEquals"
      variable = local.repository_id_key
      values   = [var.github_repository_id]
    }
  }
}

data "aws_iam_policy_document" "github_deploy_assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [local.github_oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = local.audience_key
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = local.subject_key
      values   = ["repo:${var.github_repository}:environment:${var.deploy_environment_name}"]
    }
    condition {
      test     = "StringEquals"
      variable = local.repository_owner_id_key
      values   = [var.github_repository_owner_id]
    }
    condition {
      test     = "StringEquals"
      variable = local.repository_id_key
      values   = [var.github_repository_id]
    }
  }
}

resource "aws_iam_role" "github_build" {
  name                 = var.build_role_name
  description          = "GitHub Actions role that publishes Billmesh development images"
  assume_role_policy   = data.aws_iam_policy_document.github_build_assume_role.json
  max_session_duration = 3600
}

resource "aws_iam_role" "github_deploy" {
  name                 = var.deploy_role_name
  description          = "GitHub Actions role that verifies and deploys Billmesh development images"
  assume_role_policy   = data.aws_iam_policy_document.github_deploy_assume_role.json
  max_session_duration = 3600
}

data "aws_iam_policy_document" "ecr_build" {
  statement {
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }
  statement {
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:BatchGetImage",
      "ecr:CompleteLayerUpload",
      "ecr:DescribeImageScanFindings",
      "ecr:DescribeImages",
      "ecr:GetDownloadUrlForLayer",
      "ecr:InitiateLayerUpload",
      "ecr:PutImage",
      "ecr:StartImageScan",
      "ecr:UploadLayerPart",
    ]
    resources = [local.ecr_repository_arn]
  }
}

data "aws_iam_policy_document" "ecr_deploy" {
  statement {
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }
  statement {
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:BatchGetImage",
      "ecr:DescribeImageScanFindings",
      "ecr:DescribeImages",
      "ecr:GetDownloadUrlForLayer",
      "ecr:StartImageScan",
    ]
    resources = [local.ecr_repository_arn]
  }
}

resource "aws_iam_role_policy" "github_build" {
  name   = "${var.build_role_name}-ecr"
  role   = aws_iam_role.github_build.id
  policy = data.aws_iam_policy_document.ecr_build.json
}

resource "aws_iam_role_policy" "github_deploy" {
  name   = "${var.deploy_role_name}-ecr"
  role   = aws_iam_role.github_deploy.id
  policy = data.aws_iam_policy_document.ecr_deploy.json
}

variable "aws_region" {
  type    = string
  default = "ap-south-1"
}

variable "github_repository" {
  type    = string
  default = "tociva/billmesh"
}

variable "github_repository_owner_id" {
  type = string
  validation {
    condition     = can(regex("^[1-9][0-9]*$", var.github_repository_owner_id))
    error_message = "github_repository_owner_id must be numeric."
  }
}

variable "github_repository_id" {
  type = string
  validation {
    condition     = can(regex("^[1-9][0-9]*$", var.github_repository_id))
    error_message = "github_repository_id must be numeric."
  }
}

variable "build_environment_name" {
  type    = string
  default = "ecr-build"
}

variable "deploy_environment_name" {
  type    = string
  default = "billmesh-development-api"
}

variable "ecr_repository_name" {
  type    = string
  default = "billmesh/api"
}

variable "build_role_name" {
  type    = string
  default = "billmesh-api-development-build"
}

variable "deploy_role_name" {
  type    = string
  default = "billmesh-api-development-deploy"
}

variable "create_github_oidc_provider" {
  type    = bool
  default = false
}

variable "create_ecr_repository" {
  type    = bool
  default = true
}

variable "force_delete_ecr_repository" {
  type    = bool
  default = false
}

variable "tags" {
  type    = map(string)
  default = { Project = "billmesh" }
}

output "aws_account_id" {
  value = data.aws_caller_identity.current.account_id
}

output "aws_region" {
  value = var.aws_region
}

output "ecr_repository" {
  value = var.ecr_repository_name
}

output "aws_build_role_arn" {
  value = aws_iam_role.github_build.arn
}

output "aws_deploy_role_arn" {
  value = aws_iam_role.github_deploy.arn
}
