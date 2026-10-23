# Isolated PostgreSQL tests

Database tests run only with the `integration` build tag and a dedicated local database named `budget_tracker_test`.

- Migrations must run before the test suite.
- The test URL must use localhost or a loopback IP and contain `/budget_tracker_test`. Production and remote hosts are rejected.
- The suite validates readiness, generated sqlc queries and committed versus rolled-back writes.
- No fixture or migration code drops other databases.

```sh
export TEST_DATABASE_URL='postgres://budget_test:local_test_only@127.0.0.1:5432/budget_tracker_test?sslmode=disable'
go test -tags=integration -count=1 ./tests/integration
```

GitHub Actions provisions isolated PostgreSQL 17 and runs Goose migrations before integration tests.
