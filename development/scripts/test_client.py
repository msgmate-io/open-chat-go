import json

try:
    from client.client import OpenChatPythonClient
except ModuleNotFoundError:
    from clients.oc_python_client.client.client import OpenChatPythonClient

BOT_CONFIG = {
    "temperature": 0.7,
    "max_tokens": 4096,
    "tools": [
        "get_random_number",
        "get_current_time",
        "create_confirmable_action_suggestion",
    ],
    "model": "qwen3-8b-instruct_vllm",
    "endpoint": "https://litellm.t1m.me/v1",
    "backend": "litellm",
    "context": 10,
    "system_prompt": "You are a helpful assistant.",
}

client = OpenChatPythonClient(
    host="http://localhost:1984",
    username="admin",
    password="password",
)
client.setup_bot_config(BOT_CONFIG)

user = client.login()
print("Logged in:", user.get("name"))

chat = client.create_interaction(
    message=(
        "Whats the current time?"
    )
)
print("Chat UUID:", chat.get("uuid"))

stopped_message = client.interaction_wait_for_stop_signal(wait_seconds=20)
print("Stop signal:", json.dumps(stopped_message, indent=2))

confirmations = client.get_interaction_confirmation_list(chat.get("uuid"), wait_seconds=20)
print("Confirmations:", json.dumps(confirmations, indent=2))

shared_url = client.get_shared_interaction_url(chat.get("uuid"))
print("Shared URL:", shared_url)
