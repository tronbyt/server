terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project = var.gcp_project_id
  region  = var.gcp_region
}

# Get a list of available zones in the specified region
data "google_compute_zones" "available" {
  project = var.gcp_project_id
  region  = var.gcp_region
}

# A firewall rule to allow HTTP/HTTPS traffic
resource "google_compute_firewall" "default" {
  name    = "tronbyt-server-firewall"
  network = "default"

  allow {
    protocol = "tcp"
    ports    = ["80", "443", "8000"] # Allowing 8000 for direct access
  }

  source_ranges = ["0.0.0.0/0"]
  target_tags   = ["tronbyt-server"]
}

# A persistent disk to store application data
resource "google_compute_disk" "default" {
  name = "tronbyt-server-disk"
  type = "pd-standard"
  # Use the first available zone from the data source
  zone = data.google_compute_zones.available.names[0]
  size = 1 # 1 GB, can be adjusted
}

# A GCE instance
resource "google_compute_instance" "default" {
  name         = "tronbyt-server-instance"
  machine_type = "e2-small"                       # A reasonable default
  zone         = google_compute_disk.default.zone # Must be in the same zone as the disk

  tags = ["tronbyt-server", "http-server", "https-server"]

  boot_disk {
    initialize_params {
      # Using a Debian 13 image which is common and stable
      image = "debian-cloud/debian-13"
    }
  }

  attached_disk {
    source      = google_compute_disk.default.id
    device_name = "tronbyt-data-disk" # Assign a specific device name
  }

  network_interface {
    network = "default"
    access_config {
      # Ephemeral IP
    }
  }

  # The startup script to provision the instance
  metadata_startup_script = file("${path.module}/startup.sh")

  # Pass variables to the startup script
  metadata = {
    server_port = "8000"
  }

  # Allow the instance to have full access to cloud APIs, including storage
  service_account {
    scopes = ["cloud-platform"]
  }

  depends_on = [google_compute_firewall.default]
}

# Output the full service URL
output "service_url" {
  description = "The full URL to access the Tronbyt server."
  value       = "http://${google_compute_instance.default.network_interface[0].access_config[0].nat_ip}:8000"
}
