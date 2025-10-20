# URL Shortener (Go + MySQL + Redis)

本项目整理自你提供的实现，包含：
- `POST /newurl` 创建短链
- `GET /{code}` 301 跳转
- MySQL 持久化、Redis 缓存、内存回退（DB 未配置时）

## 目录结构
```
url-shortener-go/
├─ main.go
├─ go.mod
├─ .env.example
├─ Dockerfile
└─ docker-compose.yml
```

## 快速开始（本地）

1. 安装 Go 1.20+：<https://go.dev/dl/>
2. 复制环境变量配置：
   ```bash
   cp .env.example .env
   # 按需编辑 .env
   ```
3. 启动依赖（可选，若你本地已有 MySQL/Redis 可跳过）：
   ```bash
   docker compose up -d
   ```
4. 运行服务：
   ```bash
   source .env
   go run ./main.go
   # 默认监听 :8080
   ```

### API 示例
创建短链：
```bash
curl -X POST -H "Content-Type: application/json" \
  -d '{"domain":"shortenurl.org","url":"https://www.google.com"}' \
  http://localhost:8080/newurl
```
示例返回：
```json
{
  "url": "https://www.google.com",
  "shortenUrl": "https://shortenurl.org/Ab3XyZ9Kl"
}
```

访问短链（返回 301）：
```bash
curl -I http://localhost:8080/Ab3XyZ9Kl
```

## 环境变量
- `MYSQL_DSN` 形如：`user:password@tcp(127.0.0.1:3306)/shortdb?parseTime=true&charset=utf8mb4`
- `REDIS_ADDR` 形如：`127.0.0.1:6379`
- `REDIS_PASS` Redis 密码（无则留空）
- `REDIS_DB` Redis DB 库序号（默认 0）
- `PORT` HTTP 端口，默认 `8080`

> 未配置 MySQL/Redis 时，会自动使用 **内存模式**（不持久化，仅便于本地测试）。

## 数据表
服务启动时会自动创建：
```sql
CREATE TABLE IF NOT EXISTS shortened_urls (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  domain VARCHAR(255) NOT NULL,
  code VARCHAR(9) NOT NULL,
  original_url TEXT NOT NULL,
  UNIQUE KEY unique_domain_code (domain, code)
) ENGINE=InnoDB;
```

## Docker 运行（可选）
```bash
docker build -t url-shortener-go .
docker run --env-file .env -p 8080:8080 url-shortener-go
```

## 说明
- 短码长度固定为 9，字符集 `[0-9A-Za-z]`。
- 生成短码采用 `crypto/rand`，并做了取模偏差处理。
- 读路径：Redis 命中 → MySQL 查询 → 回填缓存。
- 写路径：MySQL 插入（唯一索引兜底冲突）→ 成功后回填 Redis。

---

### 原始运行说明（你提供的文本，便于保留）
该程序可在任意 Linux/本地运行，所需环境变量：`MYSQL_DSN` / `REDIS_ADDR` / `REDIS_PASS` / `REDIS_DB` / `PORT`。若未配置 MySQL/Redis，将回退内存存储用于快速测试。
