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

variable "iso_url" {
  type    = string
  default = "https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-amd64.img"
}

variable "iso_checksum" {
  type    = string
  default = "file:https://cloud-images.ubuntu.com/releases/24.04/release/SHA256SUMS"
}

variable "username" {
  type    = string
  default = "packer"
}

variable "password" {
  type    = string
  default = "packer"
}

variable "qemu_binary" {
  type    = string
  default = "qemu-system-x86_64"
}

locals {
  build_timestamp = timestamp()
  build_directory = "build/${local.build_timestamp}"
}

source "qemu" "base" {
  vm_name          = var.vm_name
  iso_url          = var.iso_url
  iso_checksum     = var.iso_checksum
  disk_image       = true
  format           = "qcow2"
  output_directory = "${local.build_directory}"
  machine_type     = var.machine
  accelerator      = var.accelerator
  cpus             = 4
  memory           = "4096"
  headless         = true
  ssh_port         = 22
  ssh_username     = var.username
  ssh_password     = var.password
  ssh_timeout      = "900s"
  qemu_binary      = var.qemu_binary
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
EOF
  }
  qemuargs = [
    ["-cpu", "host"],
    ["-smbios", "type=1,serial=ds=nocloud-net;s=http://{{ .HTTPIP }}:{{ .HTTPPort }}/"]
  ]
  shutdown_command = "echo '${var.password}' | sudo -S shutdown -P now"
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
      "sudo systemctl enable qemu-guest-agent.service",

      "cat <<EOF | sudo tee /etc/netplan/01-netcfg.yaml",
      "network:",
      "  version: 2",
      "  ethernets:",
      "    all-virtio:",
      "      match:",
      "        driver: virtio_net",
      "      dhcp4: yes",
      "      set-name: eth0",
      "EOF",
      "sudo chmod 600 /etc/netplan/01-netcfg.yaml",
      "sudo netplan apply",
      "sudo systemctl enable systemd-networkd"
    ]
  }
}
