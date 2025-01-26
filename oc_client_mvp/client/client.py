import requests
import json

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

class OpenChatPythonClient:
    
    session_id = None
    host = None
    username = None
    password = None
    session = None
    
    def __init__(self, host="http://localhost:1984", username="admin", password="password"):
        self.host = host
        self.username = username
        self.password = password
        self.session = requests.Session()
        
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