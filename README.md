# WhatsBridge by ERPAGENT

WhatsBridge is a high-performance WhatsApp WebSocket Bridge and Dashboard designed to integrate local WhatsApp clients with remote applications (like Laravel). It features a beautiful web dashboard, automated messaging, and cloud-ready deployment on Koyeb.

## 🚀 Features

- **WhatsApp WebSocket Bridge**: Real-time messaging relay for external apps via `/ws/bridge`.
- **Beautiful Dashboard**: Modern, responsive UI for managing connections and metrics.
- **Bulk & Scheduled Messaging**: Easily send messages to multiple contacts or schedule them for later.
- **Internet Watchdog**: Automatically detects connectivity issues and reconnects to WhatsApp.
- **Laravel Integration**: Seamlessly connects with Laravel via HTTP API and WSS queue endpoints.
- **Cloud-Ready**: Deploys to Koyeb with a single Dockerfile (reads `PORT` env var).

## 🛠️ Tech Stack

- **Backend**: Go (Golang)
- **WhatsApp Library**: [whatsmeow](https://github.com/tulir/whatsmeow)
- **Frontend**: Vanilla HTML/CSS/JS with TailwindCSS CDN
- **Databases**: MySQL (usage metrics & scheduling) + PostgreSQL (WhatsApp session store)
- **Deployment**: Docker → Koyeb

## 📦 Deployment

### Koyeb (Production)

1. Push the repo and connect it to Koyeb (Docker build).
2. Set these environment variables in Koyeb:
   | Variable | Example |
   |----------|---------|
   | `MYSQL_DSN` | `user:pass@tcp(host:3306)/dbname` |
   | `WA_POSTGRES_DSN` | `postgres://user:pass@host:5432/db?sslmode=disable` |
3. Koyeb auto-injects `PORT`. The app listens on it.
4. Access the dashboard at your Koyeb public URL.

### Local Development

```powershell
$env:MYSQL_DSN = "user:pass@tcp(host:3306)/dbname"
$env:WA_POSTGRES_DSN = "postgres://user:pass@host:5432/db?sslmode=disable"
$env:WEB_PORT = "9990"  # optional, defaults to 8000
go run .
```

## 🔗 API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/status` | Bot connection status |
| POST | `/api/send` | Send a message (JSON or multipart) |
| POST | `/api/bulk-send` | Dispatch multiple messages |
| POST | `/api/schedule` | Schedule a message |
| GET | `/api/qr` | Get QR code for login |
| GET | `/api/metrics` | Usage statistics |
| POST | `/api/connect` | Reconnect the bot |
| POST | `/api/disconnect` | Disconnect the bot |
| POST | `/api/logout` | Logout and wipe session |
| WS | `/ws/bridge` | WebSocket bridge for real-time relay |

## 🔗 Laravel Integration

In your Laravel app's `.env`:
```env
WHATSBRIDGE_URL=https://your-app.koyeb.app
```

In `config/services.php`:
```php
'whatsbridge' => [
    'url' => env('WHATSBRIDGE_URL', 'http://localhost:8000'),
],
```

The Laravel `WhatsappService` sends messages via `POST {WHATSBRIDGE_URL}/api/send` with `{ "to": "...", "message": "..." }`.

## 📄 License

MIT License. See `LICENSE` for details.
