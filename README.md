# Bank Bridge

A lightweight service that polls an IMAP mailbox for bank alert emails, parses them into standardized transactions and balances stored in SQLite, and exposes both a REST API and a **[SimpleFIN](https://beta-bridge.simplefin.org/help/api)**-compatible endpoint for personal finance apps like **[Sure](https://github.com/we-promise/sure)** and **[Firefly III](https://firefly-iii.org/)**.

Many regional banks and credit unions deploy strict bot protections that block scrapers. Transaction alert emails are immediate, reliable, and immune to bot detection.

---

## Simple Usage Guide

### 1. Test an `.eml` alert file (no server needed)

With Nix:
```bash
nix run . -- -parse-file /path/to/alert.eml
```

With Go (`1.24+` / matching `go.mod`):
```bash
go run ./cmd/bank-bridge -parse-file /path/to/alert.eml
```

This prints the extracted account and transaction JSON without touching a database.

### 2. Run the server

Create a `.env` or export environment variables (for example, using a dedicated Gmail address with a Google App Password):
```bash
export PORT=8080
export DB_PATH="./data/bank-bridge.db"
export API_TOKEN="change-me-to-a-long-random-secret"
export IMAP_USER="your-burner@gmail.com"
export IMAP_PASSWORD="your-gmail-app-password"
export IMAP_SEARCH_FROM="alerts@yourbank.com"
```

Start the server:
```bash
# With Nix
nix run .

# Or build and run with Go
go build -o bank-bridge ./cmd/bank-bridge
./bank-bridge
```

Verify it is running:
```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

If you omit `IMAP_USER` / `IMAP_PASSWORD`, the server runs in API/webhook-only mode (you can push `.eml` files to `POST /api/v1/ingest/email`).

### 3. Connect your finance app

#### Option A: SimpleFIN (Sure, Firefly III, Actual, etc.)
- **Direct Access URL** (if your app accepts an Access URL directly):
  ```text
  http://bridge:YOUR_API_TOKEN@localhost:8080
  ```
  *(Omit `bridge:YOUR_API_TOKEN@` if `API_TOKEN` is unset.)*
- **SimpleFIN Setup Token** (if your app requires a base64 Setup Token):
  ```bash
  printf 'http://localhost:8080/claim?token=%s' "$API_TOKEN" | base64
  ```
  When `API_TOKEN` is configured, `/claim` and `/simplefin/claim` require authentication (`?token=`, `Authorization: Bearer ...`, or Basic auth) so your token is never exposed publicly.

#### Option B: JSON REST API
Pass `Authorization: Bearer $API_TOKEN` (or omit if `API_TOKEN` is unset):
```bash
curl -H "Authorization: Bearer $API_TOKEN" http://localhost:8080/api/v1/accounts
curl -H "Authorization: Bearer $API_TOKEN" "http://localhost:8080/api/v1/transactions?since=2026-11-01"
```

### 4. Seed or adjust balances (optional)

Transaction alert emails usually don't include your total balance. You can seed a starting balance once (subsequent parsed transactions update the running balance automatically) or ingest weekly balance summary alerts:
```bash
./bank-bridge -set-balance acc-1234 -balance 1500.00 -available 1450.00
```
Or via HTTP:
```bash
curl -X PATCH http://localhost:8080/api/v1/accounts/acc-1234/balance \
  -H "Authorization: Bearer $API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"balance": 1500.00, "available_balance": 1450.00}'
```

---

## Supported Email Formats

Parsers live in `internal/parser/` and implement the `parser.Parser` interface:
- **`gridtable`** (`internal/parser/gridtable`): Parses HTML transaction alerts containing `<table class="gridtable">` with columns for type, amount, description, date, and status, institution name in `<h1>`, and account/mask (`ACCOUNT NAME *1234`) in `<h2>` or `<strong>`.
- **`summary`** (`internal/parser/summary`): Parses HTML balance summary alerts containing `"Your current balance is <strong>$1,234.56</strong>"` and `ACCOUNT NAME *1234`.

If your bank uses a different template, add a package under `internal/parser/<bank>/`, call `parser.Register(...)` in `init()`, and blank-import it in `cmd/bank-bridge/main.go`.

---

## CLI Flags

| Flag | Description |
|---|---|
| `-parse-file <path>` | Parse a single `.eml` file, print JSON, and exit |
| `-port <port>` | Override `PORT` environment variable |
| `-db <path>` | Override `DB_PATH` environment variable |
| `-set-balance <id>` | Update balance for `<id>` in SQLite and exit (use with `-balance` and optional `-available`) |
| `-balance <amount>` | Total balance for `-set-balance` |
| `-available <amount>` | Available balance for `-set-balance` (defaults to `-balance` if omitted) |
| `-reprocess-skipped` | Re-run parsers on previously skipped emails in SQLite and exit |

---

## Configuration

Configuration is read from environment variables or `*_FILE` paths (for `agenix`, `sops-nix`, or Docker secrets):

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP server listen port |
| `DB_PATH` | `./data/bank-bridge.db` | SQLite database file path |
| `API_TOKEN` / `API_TOKEN_FILE` | `""` | Bearer/Basic/query token protecting API and SimpleFIN endpoints |
| `ACCOUNT_RULES` / `ACCOUNT_RULES_FILE` | `""` | Optional JSON overrides for account IDs, names, and balance offsets |
| `IMAP_ENABLED` | `true` if `IMAP_USER` & `IMAP_PASSWORD` set | Set `true`/`1` or `false`/`0` |
| `IMAP_HOST` | `imap.gmail.com` | IMAP server hostname |
| `IMAP_PORT` | `993` | IMAP TLS port |
| `IMAP_USER` | `""` | IMAP username / email address |
| `IMAP_PASSWORD` / `IMAP_PASSWORD_FILE` | `""` | IMAP password or app password |
| `IMAP_MAILBOX` | `INBOX` | IMAP folder to poll |
| `IMAP_SEARCH_FROM` | `""` | Optional `From` header filter (e.g. `alerts@yourbank.com`) |
| `POLL_INTERVAL` | `2m` | How often to poll IMAP (Go duration, e.g. `2m`, `30s`) |

### Custom `ACCOUNT_RULES` JSON

Rules match an account by mask, ID, or name (case-insensitive) to rename/remap accounts or apply fixed hold offsets:
```json
{
  "accounts": {
    "5678": {
      "balance_offset": 5.00
    },
    "9012": {
      "id": "acc-custom",
      "name": "HIGH YIELD SAVINGS",
      "available_offset": -500.00
    }
  }
}
```

---

## API Reference

All endpoints except `GET /health` require `API_TOKEN` (if configured) via `Authorization: Bearer <token>`, HTTP Basic auth password, or `?token=<token>`.

| Method & Path | Description |
|---|---|
| `GET /health` | Health check (`{"status":"ok"}`) |
| `GET /api/v1/accounts` | List all accounts |
| `GET /api/v1/accounts/{id}` | Get a single account |
| `POST /api/v1/accounts` | Create or update an account |
| `POST\|PUT\|PATCH /api/v1/accounts/{id}/balance` | Update `balance` and/or `available_balance` |
| `GET /api/v1/transactions` | Query transactions (`?account_id=...&since=YYYY-MM-DD\|RFC3339&limit=100&offset=0`) |
| `POST /api/v1/sync` | Trigger an asynchronous IMAP sync |
| `POST /api/v1/ingest/email` | Ingest raw RFC 822 email bytes directly |
| `GET /accounts` | SimpleFIN Read Protocol accounts & transactions (`?start-date=<unix>`) |
| `POST /claim`, `POST /simplefin/claim` | Return the SimpleFIN Access URL |

---

## NixOS Module

Add this flake to your NixOS `inputs`, then enable the hardened systemd service:

```nix
{ config, inputs, ... }: {
  imports = [
    inputs.bank-bridge.nixosModules.default
  ];

  services.bank-bridge = {
    enable = true;
    port = 8088;
    environmentFile = config.age.secrets."bank-bridge-env".path;
    # Optional: point to a separate secret file for custom account rules
    # accountRulesFile = config.age.secrets."account-rules".path;
  };
}
```

Example `environmentFile` content:
```env
API_TOKEN=change-me-to-a-long-random-secret
IMAP_USER=your-burner@gmail.com
IMAP_PASSWORD=your-app-password
IMAP_SEARCH_FROM=alerts@yourbank.com
# ACCOUNT_RULES={"accounts":{"5678":{"balance_offset":5.00},"9012":{"id":"acc-custom","available_offset":-500.00}}}
```

---

## Development & License

```bash
nix develop
go test -v ./...
```

Licensed under the [MIT License](LICENSE).
