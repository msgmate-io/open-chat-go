### MVP Open-Chat Python Client

```bash
pip install git+https://github.com/msgmate-io/open-chat-go#subdirectory=oc_client_mvp
```

```bash
python3 -m venv venv
source venv/bin/activate
```

```bash
pip install -e ./oc_client_mvp
oc_client --host http://localhost:1984 --username admin --password password --message "hi"
```
