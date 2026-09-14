#!/bin/sh
set -eu
: "${TELEGRAM_BOT_TOKEN:?Set TELEGRAM_BOT_TOKEN}"
: "${PUBLIC_BASE_URL:?Set PUBLIC_BASE_URL}"
: "${WEBHOOK_SECRET:?Set WEBHOOK_SECRET}"
PUBLIC_API="https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}"
LOCAL_API="${TELEGRAM_API_BASE:-http://telegram-bot-api:8081}/bot${TELEGRAM_BOT_TOKEN}"

echo "Logging bot out from Telegram cloud API..."
curl -fsS -X POST "${PUBLIC_API}/logOut" || true
sleep 2
echo
echo "Setting local webhook..."
curl -fsS -X POST "${LOCAL_API}/setWebhook" \
  --data-urlencode "url=${PUBLIC_BASE_URL%/}${WEBHOOK_PATH:-/telegram/webhook}" \
  --data-urlencode "secret_token=${WEBHOOK_SECRET}" \
  --data-urlencode 'drop_pending_updates=true'
echo
echo
echo "Webhook configured."
