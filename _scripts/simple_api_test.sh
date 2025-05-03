#!/bin/bash

# 1: creat test users
#
USER_A="tim+testA@timschupp.de"
USER_B="tim+testB@timschupp.de"

echo "create user A: $USER_A"
curl -X POST \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -d '{"email":"'$USER_A'","password":"password","name":"Test Tim"}' \
     http://localhost:1984/api/v1/user/register

echo "create user B: $USER_B"
curl -X POST \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -d '{"email":"'$USER_B'","password":"password","name":"Test Tim"}' \
     http://localhost:1984/api/v1/user/register

# 2: login a test users
#
SESSION_ID_A=$(curl -i -X POST \
    -H "Content-Type: application/json" \
    -H "Origin: localhost:1984" \
    -d '{"email":"'$USER_A'","password":"password"}' \
    http://localhost:1984/api/v1/user/login \
    | grep -oP 'Set-Cookie: session_id=([^;]*)' | sed 's/^.*=\(.*\)/\1/')

SESSION_ID_B=$(curl -i -X POST \
    -H "Content-Type: application/json" \
    -H "Origin: localhost:1984" \
    -d '{"email":"'$USER_B'","password":"password"}' \
    http://localhost:1984/api/v1/user/login \
    | grep -oP 'Set-Cookie: session_id=([^;]*)' | sed 's/^.*=\(.*\)/\1/')

echo "SESSION_ID_A: $SESSION_ID_A"
echo "SESSION_ID_B: $SESSION_ID_B"

# 3: fetch the user data
#
USER_A_SELF=$(curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:1984/api/v1/user/self)

USER_B_SELF=$(curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_B" \
     http://localhost:1984/api/v1/user/self)

echo "User A:"
echo $USER_A_SELF | jq
echo "User B:"
echo $USER_B_SELF | jq

# 4: add users to each others contacts
#
USER_A_CONTACT_TOKEN=$(echo $USER_A_SELF | jq -r '.contact_token')
USER_B_CONTACT_TOKEN=$(echo $USER_B_SELF | jq -r '.contact_token')
echo "User A contact token: $USER_A_CONTACT_TOKEN"
echo "User B contact token: $USER_B_CONTACT_TOKEN"

curl -X POST \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     -d '{"contact_token":"'$USER_B_CONTACT_TOKEN'"}' \
     http://localhost:1984/api/v1/contacts/add

curl -X POST \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_B" \
     -d '{"contact_token":"'$USER_A_CONTACT_TOKEN'"}' \
     http://localhost:1984/api/v1/contacts/add

echo "User A contacts:"
curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:1984/api/v1/contacts/list | jq

CONTACT_TOKEN=$(curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:1984/api/v1/contacts/list | jq -r '.rows[0].contact_token')


# List Existing Chats
curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:1984/api/v1/chats/list | jq

CHATS_LENGTH=$(curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:1984/api/v1/chats/list | jq '.rows | length')

if [ "$CHATS_LENGTH" -lt 1 ]; then
     # Create a new Chat 
     echo "Create a new Chat"
     curl -X POST \
          -H "Content-Type: application/json" \
          -H "Origin: localhost:1984" \
          -H "Cookie: session_id=$SESSION_ID_A" \
          -d '{"contact_token":"'$CONTACT_TOKEN'"}' \
          http://localhost:1984/api/v1/chats/create | jq
fi

# List The Messages inside the first chat
FIRST_CHAT=$(curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:1984/api/v1/chats/list | jq -r '.rows[0].uuid')

echo "First Chat: $FIRST_CHAT"
curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:1984/api/v1/chats/$FIRST_CHAT/messages/list | jq

# send a message
curl -X POST \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     -d '{"text":"Hello World"}' \
     http://localhost:1984/api/v1/chats/$FIRST_CHAT/messages/send | jq

# List messages again
echo "First Chat: $FIRST_CHAT"
curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:1984/api/v1/chats/$FIRST_CHAT/messages/list | jq

# print multiline string cat eof style
cat <<EOF
Connect User A:
./bin/websocat --header="Cookie: session_id=$SESSION_ID_A" ws://localhost:1984/ws/connect

Connect User B:
./bin/websocat --header="Cookie: session_id=$SESSION_ID_B" ws://localhost:1984/ws/connect

Send a Message From User A to User B:
curl -X POST \\
     -H "Content-Type: application/json" \\
     -H "Origin: localhost:1984" \\
     -H "Cookie: session_id=$SESSION_ID_A" \\
     -d '{"text":"Hello World"}' \\
     http://localhost:1984/api/v1/chats/$FIRST_CHAT/messages/send | jq
EOF

curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:1984" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:1984/api/v1/contacts/list | jq

# Try to connect to the websocket
# echo "Try to connect to the websocket"
#./bin/websocat --header="Cookie: session_id=$SESSION_ID_B" ws://localhost:1984/ws/connect