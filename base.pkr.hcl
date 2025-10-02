packer {
  required_plugins {
    qemu = {
      version = ">= 1.1.0"
      source  = "github.com/hashicorp/qemu"
    }
  }
}

variable "accelerator" {
  type    = string
  default = "kvm"
}

variable "machine" {
  type    = string
  default = "q35"
}

variable "vm_name" {
  type    = string
  default = "base"
}

variable "iso_checksum" {
  type    = string
  default = "file:https://cloud-images.ubuntu.com/releases/25.04/release/SHA256SUMS"
}

variable "username" {
  type    = string
  default = "packer"
}

variable "password" {
  type    = string
  default = "packer"
}

variable "arch" {
  type    = string
  default = "amd64"
}

locals {
  iso_url         = "https://cloud-images.ubuntu.com/releases/25.04/release/ubuntu-25.04-server-cloudimg-${var.arch}.img"
  qemu_binary     = var.arch == "arm64" ? "qemu-system-aarch64" : "qemu-system-x86_64"
  build_timestamp = timestamp()
  build_directory = "build/${local.build_timestamp}"

  # Architecture-specific arguments
  arm64_args = var.arch == "arm64" ? [["-bios", "edk2-aarch64-code.fd"]] : []
}

source "qemu" "base" {
  vm_name          = var.vm_name
  iso_url          = local.iso_url
  iso_checksum     = var.iso_checksum
  disk_image       = true
  format           = "qcow2"
  output_directory = "${local.build_directory}"
  machine_type     = var.machine
  accelerator      = var.accelerator
  cpus             = 4
  memory           = "4096"
  headless         = true
  ssh_username     = var.username
  ssh_password     = var.password
  ssh_timeout      = "900s"
  qemu_binary      = local.qemu_binary
  http_content = {
    "/meta-data" = <<EOF
EOF
    "/user-data" = <<EOF
#cloud-config
ssh_pwauth: True
users:
  - name: ${var.username}
    plain_text_passwd: ${var.password}
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    groups: sudo
    lock_passwd: false
packages: []
network:
  version: 2
  ethernets:
    all-eth:
      match:
        name: "*"
      dhcp4: true
      dhcp6: false
      optional: true
write_files:
  - path: /etc/netplan/50-cloud-init.yaml
    permissions: '0600'
    content: |
      network:
        version: 2
        ethernets:
          any-eth:
            match:
              name: "*"
            dhcp4: true
            dhcp6: false
            optional: true
runcmd:
  - rm -f /etc/netplan/50-cloud-init.yaml.dpkg*
  - netplan generate
  - netplan apply
EOF
  }
  qemuargs = concat([
    ["-cpu", "host"],
    ["-smbios", "type=1,serial=ds=nocloud-net;s=http://{{ .HTTPIP }}:{{ .HTTPPort }}/"]
  ], local.arm64_args)
  shutdown_command = "sudo -S shutdown -P now"
}

build {
  name = "base"
  sources = [
    "qemu.base"
  ]

  provisioner "shell-local" {
    inline = [
      "./qga/build.sh --out-dir qga/build --qemu-dir qapi-client/qemu"
    ]
  }

  provisioner "file" {
    source      = "qga/build"
    destination = "/tmp/"
    generated   = true
  }

  provisioner "shell" {
    inline = [
      "sudo cp -r /tmp/build/bin/qemu-ga /usr/bin/",
      "sudo cp -r /tmp/build/usr/lib/systemd/system/qemu-guest-agent.service /usr/lib/systemd/system/",
      "sudo chmod 644 /usr/lib/systemd/system/qemu-guest-agent.service",

      "sudo mkdir -p /etc/systemd/system/qemu-guest-agent.service.d",
      "echo '[Install]\\nWantedBy=multi-user.target' | sudo tee -a /etc/systemd/system/qemu-guest-agent.service.d/override.conf",

      "sudo mkdir -p /usr/var/run",

      "sudo systemctl daemon-reload",
      "sudo systemctl enable qemu-guest-agent.service"
    ]
  }
}
