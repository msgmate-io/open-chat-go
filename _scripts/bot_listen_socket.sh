#!/bin/bash

BOT_USERNAME="bot"
BOT_PASSWORD="password"

ADMIN_USERNAME="admin"
ADMIN_PASSWORD="password"

SESSION_ID=$(curl -i -X POST \
    -H "Content-Type: application/json" \
    -H "Origin: localhost:1984" \
    -d '{"email":"'$BOT_USERNAME'","password":"'$BOT_PASSWORD'"}' \
    http://localhost:1984/api/v1/user/login \
    | grep -oP 'Set-Cookie: session_id=([^;]*)' | sed 's/^.*=\(.*\)/\1/')

echo "BOT-SESSION: $SESSION_ID"

ADMIN_SESSION_ID=$(curl -i -X POST \
    -H "Content-Type: application/json" \
    -H "Origin: localhost:1984" \
    -d '{"email":"'$ADMIN_USERNAME'","password":"'$ADMIN_PASSWORD'"}' \
    http://localhost:1984/api/v1/user/login \
    | grep -oP 'Set-Cookie: session_id=([^;]*)' | sed 's/^.*=\(.*\)/\1/')

curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID" \
     http://localhost:1984/api/v1/chats/list | jq


./bin/websocat --header="Cookie: session_id=$SESSION_ID" ws://localhost:1984/ws/connect