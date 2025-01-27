import requests
import json
import re

"""

	ChatUUID   string `json:"chat_uuid"`
	SessionID  string `json:"session_id"`
	XCSRFToken string `json:"xcsrf_token"`
	APIHost    string `json:"api_host"`

client = OpenChatPythonClient(**settings.OPEN_CHAT_CREDENTIALS)
interaction = client.create_interaction(tool_init={
    "send_chat_reply": {
        "session_id" : client.session_id,
        "chat_uuid" : client.chat_uuid,
        "xcsrf_token" : client.xcsrf_token,
        "api_host" : client.host,
    }
})
"""

default_bot_config = {
    "title": "o3-mini-2025-01-31",
    "description": "OpenAI's O3 Mini, a powerful and efficient language model.",
    "configuration": {
        "temperature": 0.7,
        "max_tokens": 4096,
        "model": "o3-mini-2025-01-31",
        "endpoint": "https://api.openai.com/v1/",
        "backend": "openai",
        "context": 10,
        "system_prompt": "You are a helpful assistant.",
    }
}

class OpenChatPythonClient:
    
    session_id = None
    host = None
    username = None
    password = None
    session = None
    bot_config = default_bot_config["configuration"]

    def __init__(self, host="http://localhost:1984", username="admin", password="password"):
        self.host = host
        self.username = username
        self.password = password
        self.session = requests.Session()
        
    def setup_bot_config(self, bot_config):
        self.bot_config = bot_config
        
    def ensure_session_initialized(self):
        if (self.session_id is not None) and (self.session is not None):
            return
        self.get_session_id()
        
    def create_interaction(self, tool_init={}, message=""):
        self.ensure_session_initialized()
        
        default_bot = self.retrieve_default_bot()
        
        if default_bot is None:
            raise Exception("No default bot found")
        
        contact_token = default_bot["contact_token"]
            
        chat_create_url = f"{self.host}/api/v1/chats/create"
        chat_create_data = {
            "contact_token" : contact_token,
            "first_message" : message,
            "shared_config" : {
                **self.bot_config,
                "tool_init" : tool_init
            }
        }
        
        headers = {
            "Content-Type": "application/json",
            "Origin": self.host,
        }
        
        response = self.session.post(
            chat_create_url, 
            headers=headers, 
            cookies={"session_id": self.session_id}, 
            json=chat_create_data
        )
        
        if response.status_code == 200:
            print(f"Successfully initalized interaction!")
            return response.json()
        else:
            raise Exception(f"Failed to create chat with status code: {response.status_code}")

    def retrieve_default_bot(self):
        """
        Retrieve the default bot from the contacts list.
        
        Returns:
            dict: The default bot contact information if found, None otherwise
        """
        self.ensure_session_initialized()
        
        contacts_url = f"{self.host}/api/v1/contacts/list"
        
        headers = {
            "Content-Type": "application/json",
            "Origin": self.host
        }
        
        response = self.session.get(
            contacts_url, 
            headers=headers, 
            cookies={"session_id": self.session_id}
        )
        
        if response.status_code == 200:
            contacts_data = response.json()
            print(f"Retrieved contacts: {json.dumps(contacts_data, indent=2)}")
            
            # If there are contacts, return the first one as the default bot
            if contacts_data and "rows" in contacts_data and len(contacts_data["rows"]) > 0:
                for row in contacts_data["rows"]:
                    if row["name"] == "bot":
                        print(f"Found default bot: {row}")
                        return row
            else:
                print("No contacts found")
                return None
        else:
            print(f"Failed to retrieve contacts with status code: {response.status_code}")
            return None

    def get_session_id(self):
        """
        Login to OpenChat and establish a session.
        
        Args:
            host (str): The host URL of the OpenChat server
            username (str): Username for login (default: admin)
            password (str): Password for login (default: password)
            
        Returns:
            tuple: (session_id, requests.Session object) if successful, or (None, None) if failed
        """
        
        if self.session_id is not None: 
            return self.session_id, self.session
        
        session = requests.Session()
    
        # 1. Login to OpenChat to get a session ID
        login_url = f"{self.host}/api/v1/user/login"
        login_data = {
            "email": self.username,
            "password": self.password
        }
        
        headers = {
            "Content-Type": "application/json",
            "Origin": self.host
        }
        
        response = session.post(login_url, headers=headers, json=login_data)
        
        if response.status_code == 200:
            # Extract session_id from Set-Cookie header
            cookie_header = response.headers.get('Set-Cookie', '')
            match = re.search(r'session_id=([^;]+)', cookie_header)
            if match:
                self.session_id = match.group(1)
                self.session = session
                print(f"Successfully logged in to OpenChat!")
                return self.session_id, self.session
            else:
                print(f"Failed to extract session ID from cookie header")
                return None, None
        else:
            print(f"Login failed with status code: {response.status_code}")
            return None, None
