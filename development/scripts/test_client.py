import json

try:
    from client.client import OpenChatClient
    from client.client import InteractionStopSignal
    from client.client import PasswordAuth
except ModuleNotFoundError:
    from clients.oc_python_client.client.client import OpenChatClient
    from clients.oc_python_client.client.client import InteractionStopSignal
    from clients.oc_python_client.client.client import PasswordAuth

client = OpenChatClient(
    base_url="http://localhost:1984",
    auth=PasswordAuth(email="admin", password="password"),
)

user = client.login()
print("Logged in:", user.name)

tool_names = client.list_tool_names()
ToolNameDynamic = client.get_tool_name_enum()
print("Tool names count:", len(tool_names))
print("First 5 tools:", tool_names[:5])
print("Example enum member:", getattr(ToolNameDynamic, "GET_CURRENT_TIME", "n/a"))

bot = client.get_bot("bot")
interaction = bot.create_interaction(
    message="Whats the current time?",
    overrides={
        "tools": ["get_current_time", "create_confirmable_action_suggestion"],
        "system_prompt": "You are a helpful assistant.",
    },
)
print("Chat UUID:", interaction.uuid)

stopped_message = interaction.wait_until_finished(timeout_seconds=20)
if isinstance(stopped_message, InteractionStopSignal):
    print("Stop signal:", json.dumps(stopped_message.__dict__, indent=2))
else:
    print("Stop signal:", json.dumps(stopped_message.to_dict(), indent=2))

confirmations = interaction.confirmations(wait_seconds=20)
print("Confirmations:", json.dumps([c.__dict__ for c in confirmations], indent=2))

shared_url = interaction.shared_url()
print("Shared URL:", shared_url)
