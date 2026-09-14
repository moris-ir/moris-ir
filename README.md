Project contact: https://telegram.me/moris3245

## Large Telegram files

The official Bot API `getFile` endpoint is limited to 20MB. Telegram's official Local Bot API Server removes the download limit and supports uploads up to 2000MB when run in local mode. MORIS now includes a Docker build for that local server. See the official Telegram documentation for the current limits and requirements.

### Required for the included Local Bot API

Create API credentials at `my.telegram.org` and set:

```env
TELEGRAM_API_ID=...
TELEGRAM_API_HASH=...
TELEGRAM_LOCAL=true
TELEGRAM_API_BASE=http://telegram-bot-api:8081
MAX_FILE_SIZE_MB=2048
```

The local server is built from Telegram's official `tdlib/telegram-bot-api` source during `docker compose build`.

## Production setup

1. Copy `.env.example` to `.env`.
2. Set the bot token, API ID/hash, public HTTPS domain and strong admin/webhook secrets.
3. Put a TLS reverse proxy (Caddy, Nginx or Traefik) in front of MORIS. Telegram's Local Bot API server itself speaks HTTP; TLS termination belongs at the edge.
4. Start:

```bash
docker compose up -d --build
```

5. For webhook mode, point Telegram's webhook to:

```text
https://YOUR_DOMAIN/telegram/webhook
```

and use the same `WEBHOOK_SECRET` value in Telegram's `setWebhook` request.

## Admin

`https://YOUR_DOMAIN/admin`

The UI includes:
- luxury glassmorphism dashboard
- file search and deletion
- user analytics
- storage overview
- expiry visibility
- settings page
- responsive mobile layout

## API

```bash
curl -X POST https://YOUR_DOMAIN/api/upload \
  -F 'files=@file1.zip' \
  -F 'files=@file2.pdf'
```

## Security notes

- Never commit `.env`.
- Use a long random admin password and webhook secret.
- Keep PostgreSQL/Redis/Local Bot API off the public internet.
- Run MORIS behind HTTPS in production.
- Local Bot API requires a valid Telegram API ID/hash and must be used according to Telegram's deployment requirements.

## One-command Telegram webhook setup

After the stack is up and DNS/HTTPS is working:

```bash
set -a; . ./.env; set +a
./scripts/setup-telegram.sh
```

The script logs the bot out from Telegram's cloud Bot API and registers the webhook on the local Bot API server. Telegram documents that a bot should be logged out of the cloud API before moving it to a local server.
