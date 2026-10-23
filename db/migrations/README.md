# Goose migrations

Apply manually **before** starting the server. The server does not run migrations on startup.

```sh
go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir db/migrations postgres "$DATABASE_URL" up
```

Use a database-specific `DATABASE_URL` with appropriate privileges; use a restricted runtime role in production. Migration `00001` adds only the application schema marker and does not create financial records. Avoid rollback against databases containing important data; test rollbacks against the isolated CI database.
