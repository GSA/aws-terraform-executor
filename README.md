# AWS Terraform Executor

AWS Terraform Executor receives 'requests' to execute terraform against the provided list of accounts using a repository and version. The lambda will only execute one request per core provided to the lambda, subsequent requests will be recursively passed to further lambda invocations. Each invocation will execute all the requests it can given the number of cores. Each request will follow this flow:

 - clone the provided repository (pointing at a terraform root module) to the /tmp/`req.Name` directory
 - fetch and checkout the `req.Version`
 - create a terraform backend.tf /tmp/`req.Name` storing the state in `BUCKET`/`req.Name`.tfstate
 - execute terraform

## Repository contents

- **./**: Terraform module to deploy and configure Lambda function, S3 Bucket and IAM roles and policies
- **lambda**: Go code for Lambda function

## Terraform Module Inputs

| Name | Description | Type | Default | Required |
|------|-------------|:----:|:-----:|:-----:|
| repo_url | The HTTPS url of the terraform root module repository | string | `nil` | yes |
| project_name | The project name used as a prefix for all resources | string | `"grace"` | no |
| appenv | The targeted application environment used in resource names | string | `"development"` | no |
| region | The AWS region for executing the EC2 | string | `"us-east-1"` | no |
| cross_account_role | The name of the role to assume when running the lambda | string | `"OrganizationAccountAccessRole"` | no |
| lambda_memory | The number of megabytes of RAM to use for the inventory lambda | number | `10240` | no |
| access_logging_bucket | the S3 bucket that will receiving on-access logs for the invoice bucket | string | `""` | no |
| source_file | The full or relative path to zipped binary of lambda handler | string | `"../release/aws-terraform-executor.zip"` | no |
| git_token | The Auth token to pass for authenticating to the repository | string | `""` | no |


[top](#top)

## Terraform Output Variables

| Name                 | Description |
| -------------------- | ------------|
| lambda_arn           | The ARN of the created Lambda |
| lambda_name          | The name of the created Lambda |


[top](#top)

## Environment Variables

### Lambda Environment Variables

| Name                 | Description |
| -------------------- | ------------|
| REGION               | (optional) Region used for EC2 instances (default: us-east-1) |
| BUCKET               | (required) Name of the bucket for storing terraform state |
| REPO_URL             | (required) The HTTPS url of the terraform root module repository |
| ROLE_NAME            | (optional) The role to assume before executing terraform in the provided account |
| GIT_TOKEN            | (optional) The Auth token to pass for authenticating to the repository |


[top](#top)


## Public domain

This project is in the worldwide [public domain](LICENSE.md). As stated in [CONTRIBUTING](CONTRIBUTING.md):

> This project is in the public domain within the United States, and copyright and related rights in the work worldwide are waived through the [CC0 1.0 Universal public domain dedication](https://creativecommons.org/publicdomain/zero/1.0/).
>
> All contributions to this project will be released under the CC0 dedication. By submitting a pull request, you are agreeing to comply with this waiver of copyright interest.test
