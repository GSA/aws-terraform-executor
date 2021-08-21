data "aws_iam_policy_document" "role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]
    principals {
      type = "Service"
      identifiers = [
        "lambda.amazonaws.com",
      ]
    }
  }
}
#tfsec:ignore:AWS099
data "aws_iam_policy_document" "policy" {
  statement {
    effect = "Allow"
    actions = [
      "lambda:InvokeFunction"
    ]
    resources = ["arn:aws:lambda:${var.region}:${local.account_id}:function:${local.app_name}"]
  }
  statement {
    effect = "Allow"
    actions = [
      "s3:GetBucketLocation",
      "s3:ListBucket"
    ]
    resources = [aws_s3_bucket.bucket.arn]
  }
  statement {
    effect = "Allow"
    actions = [
      "s3:PutObject",
      "s3:GetObject",
      "s3:DeleteObject"
    ]
    resources = [
      "${aws_s3_bucket.bucket.arn}/*"
    ]
  }
  statement {
    effect = "Allow"
    actions = [
      "kms:Encrypt",
      "kms:Decrypt",
      "kms:ReEncrypt*",
      "kms:GenerateDataKey*",
      "kms:DescribeKey"
    ]
    resources = [aws_kms_key.kms.arn]
  }
  statement {
    effect = "Allow"
    actions = [
      "logs:PutLogEvents",
      "logs:DescribeLogStreams",
      "logs:DescribeLogGroups",
      "logs:CreateLogStream",
      "logs:CreateLogGroup"
    ]
    resources = ["*"]
  }
  statement {
    effect = "Allow"
    actions = [
      "sts:AssumeRole"
    ]
    resources = ["arn:aws:iam::*:role/${var.cross_account_role}"]
  }
}


resource "aws_iam_role" "role" {
  name               = local.app_name
  assume_role_policy = data.aws_iam_policy_document.role.json
}
resource "aws_iam_policy" "policy" {
  name        = local.app_name
  description = "Policy to lambda permissions ${local.app_name}"
  policy      = data.aws_iam_policy_document.policy.json
}

resource "aws_iam_role_policy_attachment" "policy" {
  role       = aws_iam_role.role.name
  policy_arn = aws_iam_policy.policy.arn
}
