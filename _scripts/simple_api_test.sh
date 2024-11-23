#!/bin/bash

# 1: creat test users
#
USER_A="tim+testA@timschupp.de"
USER_B="tim+testB@timschupp.de"

echo "create user A: $USER_A"
curl -X POST \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:8080" \
     -d '{"email":"'$USER_A'","password":"password","name":"Test Tim"}' \
     http://localhost:8080/api/v1/user/register

echo "create user B: $USER_B"
curl -X POST \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:8080" \
     -d '{"email":"'$USER_B'","password":"password","name":"Test Tim"}' \
     http://localhost:8080/api/v1/user/register

# 2: login a test users
#
SESSION_ID_A=$(curl -i -X POST \
    -H "Content-Type: application/json" \
    -H "Origin: localhost:8080" \
    -d '{"email":"'$USER_A'","password":"password"}' \
    http://localhost:8080/api/v1/user/login \
    | grep -oP 'Set-Cookie: session_id=([^;]*)' | sed 's/^.*=\(.*\)/\1/')

SESSION_ID_B=$(curl -i -X POST \
    -H "Content-Type: application/json" \
    -H "Origin: localhost:8080" \
    -d '{"email":"'$USER_B'","password":"password"}' \
    http://localhost:8080/api/v1/user/login \
    | grep -oP 'Set-Cookie: session_id=([^;]*)' | sed 's/^.*=\(.*\)/\1/')

echo "SESSION_ID_A: $SESSION_ID_A"
echo "SESSION_ID_B: $SESSION_ID_B"

# 3: fetch the user data
#
USER_A_SELF=$(curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:8080" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:8080/api/v1/user/self)

USER_B_SELF=$(curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:8080" \
     -H "Cookie: session_id=$SESSION_ID_B" \
     http://localhost:8080/api/v1/user/self)

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
     -H "Origin: localhost:8080" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     -d '{"contact_token":"'$USER_B_CONTACT_TOKEN'"}' \
     http://localhost:8080/api/v1/contacts/add

curl -X POST \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:8080" \
     -H "Cookie: session_id=$SESSION_ID_B" \
     -d '{"contact_token":"'$USER_A_CONTACT_TOKEN'"}' \
     http://localhost:8080/api/v1/contacts/add

echo "User A contacts:"
curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:8080" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:8080/api/v1/contacts/list | jq

CONTACT_TOKEN=$(curl -X GET \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:8080" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:8080/api/v1/contacts/list | jq -r '.rows[0].contact_token')

# Create a new Chat 
echo "User B contact token: $CONTACT_TOKEN"
curl -X POST \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:8080" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     -d '{"contact_token":"'$CONTACT_TOKEN'"}' \
     http://localhost:8080/api/v1/chats/create | jq

# List Existing Chats
curl -X POST \
     -H "Content-Type: application/json" \
     -H "Origin: localhost:8080" \
     -H "Cookie: session_id=$SESSION_ID_A" \
     http://localhost:8080/api/v1/chats/list | jq