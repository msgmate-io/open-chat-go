from clients.oc_python_client.client.client import OpenChatPythonClient

BOT_CONFIG = {
    "temperature": 0.7,
    "max_tokens": 4096,
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

chat = client.create_interaction(message="Hello from Python")
print("Chat UUID:", chat.get("uuid"))

shared_url = client.get_shared_interaction_url(chat.get("uuid"))
print("Shared URL:", shared_url)
