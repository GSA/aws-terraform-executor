locals {
  useAccessLogging = length(var.access_logging_bucket) > 0 ? [1] : []
}

resource "aws_s3_bucket" "bucket" {
  bucket        = local.app_name
  acl           = "private"
  force_destroy = true

  versioning {
    enabled = true
  }

  #tfsec:ignore:AWS002
  dynamic "logging" {
    for_each = local.useAccessLogging
    content {
      target_bucket = var.access_logging_bucket
      target_prefix = "${local.app_name}-logs/"
    }
  }

  server_side_encryption_configuration {
    rule {
      apply_server_side_encryption_by_default {
        kms_master_key_id = aws_kms_key.kms.arn
        sse_algorithm     = "aws:kms"
      }
    }
  }
}

resource "aws_s3_bucket_public_access_block" "bucket" {
  bucket = aws_s3_bucket.bucket.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}
