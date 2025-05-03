## TLS management within open chat

Open chat request and manage it's own TLS certificates.

```bash
backend client login # enter admin user login credentials
backend client tls --hostname <hostname> --key-prefix <key-prefix>
```


```bash
backend client login 
backend client tls --hostname "msgmate.io" --key-prefix "msgmate"
```
