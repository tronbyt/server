variable "gcp_project_id" {
  description = "The Google Cloud project ID to deploy to."
  type        = string
}

variable "gcp_region" {
  description = "The Google Cloud region to deploy the service in (e.g., 'us-central1')."
  type        = string
  default     = "us-central1"
}
