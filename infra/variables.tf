variable "tenancy_ocid" {
  type        = string
  description = "OCID da tenancy"
}

variable "user_ocid" {
  type        = string
  description = "OCID do usuário"
}

variable "fingerprint" {
  type        = string
  description = "Fingerprint da API key (mostrado no Console ao subir a chave pública)"
}

variable "private_key_path" {
  type        = string
  description = "Caminho da chave privada de API"
  default     = "./oci_api_key.pem"
}

variable "region" {
  type    = string
  default = "sa-saopaulo-1"
}

variable "ssh_public_key" {
  type        = string
  description = "Conteúdo da chave SSH pública para acesso à VM"
}
