resource "aws_lambda_function" "lambda" {
  filename                       = var.source_file
  function_name                  = local.app_name
  description                    = "Executes terraform against provided accounts"
  role                           = aws_iam_role.role.arn
  handler                        = "function/lambda_function.lambda_handler"
  source_code_hash               = filebase64sha256(var.source_file)
  kms_key_arn                    = aws_kms_key.kms.arn
  reserved_concurrent_executions = -1
  memory_size                    = var.lambda_memory
  runtime                        = "go1.x"
  timeout                        = 900

  environment {
    variables = {
      REGION    = var.region
      BUCKET    = aws_s3_bucket.bucket.id
      GIT_TOKEN = var.git_token
      REPO_URL  = var.repo_url
    }
  }

  depends_on = [aws_iam_role_policy_attachment.policy]
}
