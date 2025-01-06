provider "aws" {
  region = "us-east-1"
}

resource "aws_iam_user" "envoy_ai_gateway_ci_user" {
  name = "envoy_ai_gateway_ci_user"
}

resource "aws_iam_policy" "bedrock_invoke_policy" {
  name        = "envoy_ai_gateway_bedrock_invoke_policy"
  description = "Allows invoking AWS Bedrock models"

  policy = jsonencode({
    "Version" : "2012-10-17",
    "Statement" : [
      {
        "Effect" : "Allow",
        "Action" : [
          "bedrock:InvokeModel",
          "bedrock:InvokeModelWithResponseStream"
        ],
        "Resource" : "*"
      }
    ]
  })
}

resource "aws_iam_access_key" "bedrock_user" {
  user = aws_iam_user.envoy_ai_gateway_ci_user.name
}

resource "aws_iam_user_policy_attachment" "ai_gateway_ci_user_bedrock_policy_attachment" {
  user       = aws_iam_user.envoy_ai_gateway_ci_user.name
  policy_arn = aws_iam_policy.bedrock_invoke_policy.arn
}

output "access_key_id" {
  value       = aws_iam_access_key.bedrock_user.id
  sensitive   = true
  description = "The access key ID for the AWS Bedrock user"
}

output "secret_access_key" {
  value       = aws_iam_access_key.bedrock_user.secret
  sensitive   = true
  description = "The secret access key for the AWS Bedrock user"
}
