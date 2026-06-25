import json

try:
    from client.client import OpenChatPythonClient
    from client.client import InteractionStopSignal
    from client.generated_api.types import Unset
except ModuleNotFoundError:
    from clients.oc_python_client.client.client import OpenChatPythonClient
    from clients.oc_python_client.client.client import InteractionStopSignal
    from clients.oc_python_client.client.generated_api.types import Unset

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
print("Logged in:", None if isinstance(user.name, Unset) else user.name)

chat = client.create_interaction(
    message=(
        "Whats the current time?"
    )
)
chat_uuid = "" if isinstance(chat.uuid, Unset) else chat.uuid
print("Chat UUID:", chat_uuid)

stopped_message = client.interaction_wait_for_stop_signal(wait_seconds=20)
if isinstance(stopped_message, InteractionStopSignal):
    print("Stop signal:", json.dumps(stopped_message.__dict__, indent=2))
else:
    print("Stop signal:", json.dumps(stopped_message.to_dict(), indent=2))

confirmations = client.get_interaction_confirmation_list(chat_uuid, wait_seconds=20)
print("Confirmations:", json.dumps([c.__dict__ for c in confirmations], indent=2))

shared_url = client.get_shared_interaction_url(chat_uuid)
print("Shared URL:", shared_url)
