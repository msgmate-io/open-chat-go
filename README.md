## open-chat-go (mvp)

Rewrite of the [msgmate open-chat spec, but in go](https://beta.msgmate.io/api/schema/swagger-ui/).
Aim is portability and performance to enable planned p2p features.

### test

```bash
go test -v ./...
```

### build

```bash
go build -ldflags "-s -w"
```

### non standart lib packages

- `github.com/urfave/cli/v3` for cli
- `github.com/rs/cors` for cors
- `github.com/golang-jwt/jwt/v5` for jwt's