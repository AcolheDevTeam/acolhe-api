# Imagem Ubuntu 24.04 para o shape micro (x86/AMD).
data "oci_core_images" "ubuntu" {
  compartment_id           = var.tenancy_ocid
  operating_system         = "Canonical Ubuntu"
  operating_system_version = "24.04"
  shape                    = "VM.Standard.E2.1.Micro"
  sort_by                  = "TIMECREATED"
  sort_order               = "DESC"
}

# Duas VMs Always Free: staging e prod (o free tier permite 2 micros AMD).
# Micro é shape FIXO (1 OCPU / 1GB) — não aceita shape_config.
resource "oci_core_instance" "vm" {
  for_each = toset(["staging", "prod"])

  compartment_id      = var.tenancy_ocid
  availability_domain = data.oci_identity_availability_domains.ads.availability_domains[0].name
  display_name        = "acolhe-${each.key}"
  shape               = "VM.Standard.E2.1.Micro"

  source_details {
    source_type = "image"
    source_id   = data.oci_core_images.ubuntu.images[0].id
  }

  create_vnic_details {
    subnet_id        = oci_core_subnet.subnet.id
    assign_public_ip = true
  }

  metadata = {
    ssh_authorized_keys = var.ssh_public_key
    user_data           = base64encode(file("${path.module}/cloud-init.yaml"))
  }
}

output "public_ips" {
  value = { for k, vm in oci_core_instance.vm : k => vm.public_ip }
}

output "ssh_commands" {
  value = { for k, vm in oci_core_instance.vm : k => "ssh ubuntu@${vm.public_ip}" }
}
