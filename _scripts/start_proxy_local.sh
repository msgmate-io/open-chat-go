#!/bin/bash

SERVER_IP="89.58.25.188"
P1_SERVER="$SERVER_IP:1984"

ROOT_USER_SESSION_P1=$(curl -i -X POST \
    -H "Content-Type: application/json" \
    -H "Origin: $P1_SERVER" \
    -d '{"email":"admin","password":"password"}' \
    http://$P1_SERVER/api/v1/user/login \
    | grep -oP 'Set-Cookie: session_id=([^;]*)' | sed 's/^.*=\(.*\)/\1/')

echo "ROOT_USER_SESSION_P1: $ROOT_USER_SESSION_P1"

# List all the server nodes
# 
GET_NODES=$(curl -i -X GET \
    -H "Content-Type: application/json" \
    -H "Origin: $P1_SERVER" \
    -H "Cookie: session_id=$ROOT_USER_SESSION_P1" \
    http://$P1_SERVER/api/v1/server/nodes)