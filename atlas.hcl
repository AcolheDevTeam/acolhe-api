// Atlas — schema.sql é a fonte da verdade; as migrations são geradas a partir dele.
// O dev-url usa um Postgres efêmero em Docker para o Atlas calcular o diff e rodar
// o lint de segurança (colunas dropadas com dados, NOT NULL sem default, locks).

variable "database_url" {
  type    = string
  default = getenv("DATABASE_URL")
}

env "local" {
  // Estado desejado: o schema declarativo.
  src = "file://internal/db/schema.sql"

  // Banco de trabalho efêmero usado pelo Atlas (requer Docker).
  dev = "docker://postgres/16/dev?search_path=public"

  // Banco real onde as migrations são aplicadas.
  url = var.database_url

  migration {
    dir = "file://internal/db/migrations"
  }

  // Lint: falha o CI em operações destrutivas/perigosas (justificativa LGPD §3.4).
  lint {
    destructive {
      error = true
    }
    data_depend {
      error = true
    }
  }
}
