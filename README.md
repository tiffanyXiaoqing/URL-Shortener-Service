# URL Shortener (Go + MySQL + Redis)

This project is based on your provided implementation and includes:
- `POST /newurl` to create short URLs
- `GET /{code}` to perform a 301 redirect
- Persistent storage via MySQL, caching via Redis, and in-memory fallback (when DB is not configured)

## Project Structure
```
url-shortener-go/
├─ main.go
├─ go.mod
├─ .env.example
├─ Dockerfile
└─ docker-compose.yml
```

## Quick Start (Local)

1. Install Go 1.20+: <https://go.dev/dl/>
2. Copy and configure environment variables:
   ```bash
   cp .env.example .env
   # Edit .env as needed
   ```
3. Start dependencies (optional if you already have MySQL/Redis running locally):
   ```bash
   docker compose up -d
   ```
4. Run the service:
   ```bash
   source .env
   go run ./main.go
   # By default, the service listens on :8080
   ```

### API Example
Create a short URL:
```bash
curl -X POST -H "Content-Type: application/json" \
  -d '{"domain":"shortenurl.org","url":"https://www.google.com"}' \
  http://localhost:8080/newurl
```
Sample response:
```json
{
  "url": "https://www.google.com",
  "shortenUrl": "https://shortenurl.org/Ab3XyZ9Kl"
}
```

Redirect using short URL:
```bash
curl -I http://localhost:8080/Ab3XyZ9Kl
```

## Environment Variables
- `MYSQL_DSN` Example: `user:password@tcp(127.0.0.1:3306)/shortdb?parseTime=true&charset=utf8mb4`
- `REDIS_ADDR` Example: `127.0.0.1:6379`
- `REDIS_PASS` Redis password (leave blank if not set)
- `REDIS_DB` Redis DB index (default: 0)
- `PORT` HTTP port (default: `8080`)

> If MySQL or Redis is not configured, the service will fall back to **in-memory mode** (no persistence), useful for local testing.

## Database Schema
The table is created automatically on startup:
```sql
CREATE TABLE IF NOT EXISTS shortened_urls (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  domain VARCHAR(255) NOT NULL,
  code VARCHAR(9) NOT NULL,
  original_url TEXT NOT NULL,
  UNIQUE KEY unique_domain_code (domain, code)
) ENGINE=InnoDB;
```

## Run with Docker (Optional)
```bash
docker build -t url-shortener-go .
docker run --env-file .env -p 8080:8080 url-shortener-go
```

## Notes
- Short codes are fixed at 9 characters, using `[0-9A-Za-z]`.
- Codes are generated using `crypto/rand` with bias correction on modulo operations.
- Read flow: Redis hit → fallback to MySQL → then b