# MySQL CDC to HTTP

Read about [CDC (Change Data Capture)](https://en.wikipedia.org/wiki/Change_data_capture) if you don't know what it is.

This binary can capture all MySQL/MariaDB changes and send to specific URL.

## Prerequisites

- MySQL or MySQL-compatible DB like MariaDB or Vitess.
- Redis or Redis-compatible DB like Valkey or DragonFly.

## How to run for development?

Copy these files then adjust the values if necessary:
- `.env.example` → `.env`,
- `config/table_groups.example.yaml` → `config/table_groups.yaml`

Run the logger to debug:

```sh
go run logger/logger.go
```

Then, run the CDC:

```sh
go run main.go
```

The MySQL CDC will listen all changes based on configuration that already set on `.env`

## How to run with Docker?

```sh
docker run -v ./.env:/app/.env mul14/mysql-cdc-to-http
```
