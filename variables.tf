variable "project_name" {
  type        = string
  description = "(optional) The project name used as a prefix for all resources"
  default     = "grace"
}

variable "cross_account_role" {
  type        = string
  description = "(optional) The name of the role to assume when running the lambda"
  default     = "OrganizationAccountAccessRole"
}

variable "repo_url" {
  type        = string
  description = "The HTTPS url of the terraform root module repository"
}

variable "git_token" {
  type        = string
  description = "The Auth token to pass for authenticating to the repository"
}

variable "appenv" {
  type        = string
  description = "(optional) The targeted application environment used in resource names (default: development)"
  default     = "development"
}

variable "region" {
  type        = string
  description = "(optional) The AWS region for executing the EC2 (default: us-east-1)"
  default     = "us-east-1"
}

variable "source_file" {
  type        = string
  description = "(optional) full or relative path to zipped binary of lambda handler"
  default     = "../release/aws-terraform-executor.zip"
}

variable "access_logging_bucket" {
  type        = string
  description = "(optional) the S3 bucket that will receiving on-access logs for the invoice bucket"
  default     = ""
}

variable "lambda_memory" {
  type        = number
  description = "(optional) The number of megabytes of RAM to use for the inventory lambda"
  default     = 10240
}
