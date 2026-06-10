import argparse
from typing import Any, Dict, Optional

import requests


DEFAULT_BOT_CONFIG = {
    "temperature": 0.7,
    "max_tokens": 4096,
    "model": "o3-mini-2025-01-31",
    "endpoint": "https://api.openai.com/v1/",
    "backend": "openai",
    "context": 10,
    "system_prompt": "You are a helpful assistant.",
}


class OpenChatPythonClient:
    def __init__(self, host: str = "http://localhost:1984", username: str = "admin", password: str = "password"):
        self.host = host.rstrip("/")
        self.username = username
        self.password = password
        self.session = requests.Session()
        self.bot_config = dict(DEFAULT_BOT_CONFIG)
        self._logged_in = False

    def setup_bot_config(self, bot_config: Dict[str, Any]) -> None:
        self.bot_config = dict(bot_config)

    def login(self) -> Dict[str, Any]:
        if self._logged_in:
            return self.get_user_self()

        cookie_domain = self.host.replace("http://", "").replace("https://", "").split(":")[0]
        login_url = f"{self.host}/api/v1/user/login?cookie_domain={cookie_domain}"
        response = self.session.post(
            login_url,
            json={"email": self.username, "password": self.password},
            headers={"Content-Type": "application/json", "Origin": self.host},
            timeout=20,
        )
        response.raise_for_status()

        payload = response.json() if response.content else {}
        self._logged_in = True
        if isinstance(payload, dict) and payload.get("user"):
            return payload["user"]
        return self.get_user_self()

    def ensure_session_initialized(self) -> None:
        if not self._logged_in:
            self.login()

    def get_user_self(self) -> Dict[str, Any]:
        response = self.session.get(
            f"{self.host}/api/v1/user/self",
            headers={"Content-Type": "application/json", "Origin": self.host},
            timeout=20,
        )
        response.raise_for_status()
        return response.json()

    def retrieve_default_bot(self) -> Optional[Dict[str, Any]]:
        self.ensure_session_initialized()
        response = self.session.get(
            f"{self.host}/api/v1/contacts/list",
            headers={"Content-Type": "application/json", "Origin": self.host},
            timeout=20,
        )
        response.raise_for_status()
        rows = response.json().get("rows", [])
        for row in rows:
            if row.get("name") == "bot":
                return row
        return None

    def create_interaction(self, tool_init: Optional[Dict[str, Any]] = None, message: str = "") -> Dict[str, Any]:
        self.ensure_session_initialized()
        default_bot = self.retrieve_default_bot()
        if not default_bot:
            raise RuntimeError("No default bot contact found")

        payload = {
            "contact_token": default_bot["contact_token"],
            "first_message": message,
            "shared_config": {
                **self.bot_config,
                "tool_init": tool_init or {},
            },
        }

        response = self.session.post(
            f"{self.host}/api/v1/chats/create",
            json=payload,
            headers={"Content-Type": "application/json", "Origin": self.host},
            timeout=30,
        )
        response.raise_for_status()
        return response.json()

    def publish_chat(self, chat_uuid: str) -> Dict[str, Any]:
        self.ensure_session_initialized()
        response = self.session.post(
            f"{self.host}/api/chat/{chat_uuid}/publish",
            headers={"Content-Type": "application/json", "Origin": self.host},
            timeout=20,
        )
        response.raise_for_status()
        return response.json()

    def get_shared_interaction_url(self, chat_uuid: str) -> str:
        publish_data = self.publish_chat(chat_uuid)
        share_uuid = publish_data.get("chat_share_uuid")
        if not share_uuid:
            raise RuntimeError("publish endpoint did not return chat_share_uuid")
        return f"{self.host}/interaction/{share_uuid}"


def main() -> None:
    parser = argparse.ArgumentParser(description="MVP Open-Chat Python client")
    parser.add_argument("--host", default="http://localhost:1984")
    parser.add_argument("--username", default="admin")
    parser.add_argument("--password", default="password")
    parser.add_argument("--message", default="Hello from oc_client_mvp")
    args = parser.parse_args()

    client = OpenChatPythonClient(host=args.host, username=args.username, password=args.password)
    user = client.login()
    print(f"Logged in as: {user.get('name', 'unknown')}")
    chat = client.create_interaction(message=args.message)
    print(f"Created interaction chat: {chat.get('uuid', '<unknown>')}")


if __name__ == "__main__":
    main()
