This contains the basic example manifests to create an Envoy AI Gateway that handles
traffic for various AI providers.

## Examples

### General Examples
- `basic.yaml` - Basic configuration without any backends
- `openai.yaml` - OpenAI integration
- `azure_openai.yaml` - Azure OpenAI integration
- `gcp_vertex.yaml` - GCP Vertex AI integration
- `tars.yaml` - TARS integration
- `cohere.yaml` - Cohere integration

### AWS Bedrock Examples

#### Basic Authentication
- `aws.yaml` - AWS Bedrock with static credentials (not recommended for production)
- `aws-irsa.yaml` - AWS Bedrock with IRSA (IAM Roles for Service Accounts) - for EKS
- `aws-pod-identity.yaml` - AWS Bedrock with EKS Pod Identity - for EKS 1.24+

#### Per-User Cost Attribution (Advanced)
Per-user cost attribution using STS AssumeRole with session tags for granular cost tracking:
- `aws-per-user-cost-attribution.yaml` - Using IRSA (EKS with OIDC provider)
- `aws-per-user-cost-attribution-pod-identity.yaml` - Using EKS Pod Identity (EKS 1.24+)
- `aws-per-user-cost-attribution-ec2.yaml` - Using EC2 Instance Roles (self-managed K8s or standalone)
- `aws-per-user-cost-attribution-ecs.yaml` - Using ECS Task Roles (AWS ECS/Fargate)

## Recommendations

### For AWS Bedrock

**Basic Usage:**
- **EKS 1.24+**: Use `aws-pod-identity.yaml` (simpler, recommended)
- **EKS < 1.24**: Use `aws-irsa.yaml`
- **Self-managed K8s on EC2**: Use `aws.yaml` with environment variables or EC2 instance roles
- **ECS/Fargate**: Use ECS task roles (see ECS example)
- **Development only**: Use `aws.yaml` with static credentials

[AWS Best Practices for Pod Identity](https://docs.aws.amazon.com/eks/latest/best-practices/identity-and-access-management.html#_identities_and_credentials_for_eks_pods)

**Per-User Cost Attribution:**

Implements [AWS Scenario 4: Per-user tracking through an LLM gateway](https://aws.amazon.com/blogs/machine-learning/granular-cost-attribution-for-amazon-bedrock/)

Choose based on your platform:
- **EKS 1.24+**: Use `aws-per-user-cost-attribution-pod-identity.yaml`
- **EKS < 1.24**: Use `aws-per-user-cost-attribution.yaml` (IRSA)
- **Self-managed K8s on EC2**: Use `aws-per-user-cost-attribution-ec2.yaml`
- **ECS/Fargate**: Use `aws-per-user-cost-attribution-ecs.yaml`

Benefits of per-user cost attribution:
- Track costs by user, team, department, or tenant
- Session tags appear in AWS Cost Explorer and CUR 2.0
- No need to create hundreds of IAM users
- Centralized credential management via ext-auth service

### For Other Providers
- **OpenAI**: Use `openai.yaml` with API key
- **Azure OpenAI**: Use `azure_openai.yaml` with API key
- **GCP Vertex AI**: Use `gcp_vertex.yaml` with service account
- **Cohere**: Use `cohere.yaml` with API key
- **TARS**: Use `tars.yaml`
