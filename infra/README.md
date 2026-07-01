# infra — Terraform (Oracle Cloud Always Free)

IaC das duas VMs que hospedam a `acolhe-api`:

| Ambiente | Branch    | VM (shape)               | Tag da imagem |
|----------|-----------|--------------------------|---------------|
| staging  | `develop` | `VM.Standard.E2.1.Micro` | `:develop`    |
| prod     | `main`    | `VM.Standard.E2.1.Micro` | `:latest`     |

Ambas Always Free (1 OCPU / 1GB, x86), região São Paulo. Cada VM roda o stack
completo via Docker Compose (Postgres + Redis + API + worker) — ver `../deploy/`.
**A VM nunca compila:** o build é no GitHub Actions e a imagem vem pronta do GHCR.

## O que o Terraform cria
- VCN + subnet pública + internet gateway + route table
- Security List liberando **22 / 80 / 443** (Postgres/Redis ficam internos)
- 2 instâncias `E2.1.Micro` (staging e prod) com Ubuntu 24.04
- `cloud-init`: swap 4G, libera iptables 80/443, instala Docker + Compose

## Pré-requisito (uma vez, no Console da Oracle)
Gerar a chave de API e subir a pública em **My profile → API keys**, depois preencher
`terraform.tfvars` (tenancy/user/fingerprint/region/ssh_public_key). Nunca commitar chaves.

## Subir / alterar infra
```bash
terraform init
terraform apply
terraform output   # mostra os IPs de staging e prod
```

## Bootstrap das VMs (uma vez, após criar)
`vm-bootstrap.local.sh` (gitignored, gerado localmente) instala a chave de deploy do
GitHub Actions em cada VM e cria o `~/acolhe/.env` com os segredos. Roda da sua máquina
com sua chave SSH pessoal.

## Deploy
Automático: push em `develop` → staging, push em `main` → production
(ver `../.github/workflows/deploy.yml`). O Actions builda, publica no GHCR e faz
`docker compose pull && up -d` por SSH na VM do ambiente.

> `terraform.tfvars`, `*.pem`, `*.tfstate*`, `*.env` e `*.local.sh` estão no `.gitignore` — **nunca commitar**.
