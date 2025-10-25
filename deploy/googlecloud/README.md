# Deploy Tronbyt-Server to Google Cloud

This guide will walk you through deploying the application to Google Cloud Run using the provided Terraform files.
Your only task is to provide your specific Google Cloud project details in a single file and then run three commands.

## Prerequisites

Before you begin, you must have the following tools installed and configured on your local machine:

1. **Terraform CLI**: [Download and install Terraform](https://developer.hashicorp.com/terraform/tutorials/aws-get-started/install-cli). Homebrew: `brew install terraform`.

3. **Google Cloud CLI (gcloud)**: [Download and install the Google Cloud SDK](https://cloud.google.com/sdk/docs/install). Homebrew: `brew install gcloud-cli`.

### Critical Step: Authenticate with Google Cloud

You must authenticate your local environment so Terraform has permission to create resources in your account.
Run the following command in your terminal and follow the browser prompts to log in to your Google account:

```sh
gcloud auth application-default login
```

## Configure Your Deployment

You only need to edit one file, `terraform.tfvars`, to tell Terraform which project to deploy to.

Set `gcp_project_id` to your actual Google Cloud Project ID and `gcp_region` to the region to deploy to.

See https://console.cloud.google.com/compute/zones for the available regions.

## Deploy the Service

Open your terminal and navigate to the directory containing this file.
Run the following commands in order.

### Step 1: Initialize Terraform

This command prepares your directory and downloads the necessary Google Cloud plugin. You only need to run this once.

```sh
terraform init
```

### Step 2: Apply the Configuration

This command creates and deploys all the resources defined in the configuration.

```sh
terraform apply
```

Terraform will show you a plan of what it will create. When prompted, type yes and press **Enter** to approve the deployment.

## Access Your Service

After the apply command finishes (it may take a minute or two), Terraform will print the public URL for your new service.
Look for the Outputs: section in your terminal:

```
Apply complete\! Resources: 3 added, 0 changed, 0 destroyed.

Outputs:

service\_url \= "http://aa.bb.cc.dd:8000"
```

After a few minutes, you can access your running application by visiting the `service\_url` in your web browser.

## How to Remove the Service (Optional)

If you no longer need the service, you can remove all the resources created by this template by running a single command from the same directory:

```sh
terraform destroy
```

Type `yes` when prompted to confirm the deletion.
