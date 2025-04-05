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
        "create_confirmable_action_suggestion",
    ],
    "model": "deepseek-ai/DeepSeek-V4-Flash",
    "endpoint": "https://api.deepinfra.com/v1/openai",
    "backend": "deepinfra",
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
        "Generate a random number between 10 and 40, "
        "but do not call get_random_number directly. "
        "First call create_confirmable_action_suggestion with "
        "target_tool_name=get_random_number, suggested_inputs={\"min\":10,\"max\":40}, "
        "title='Generate random number', description='Generate a random number between 10 and 40', "
        "confirm_label='Confirm', danger_level='low'."
    )
)
print("Chat UUID:", chat.get("uuid"))

confirmations = client.get_interaction_confirmation_list(chat.get("uuid"), wait_seconds=20)
print("Confirmations:", json.dumps(confirmations, indent=2))

shared_url = client.get_shared_interaction_url(chat.get("uuid"))
print("Shared URL:", shared_url)
