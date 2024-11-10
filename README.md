## open-chat-go (mvp)

Rewrite of the [msgmate open-chat spec, but in go](https://beta.msgmate.io/api/schema/swagger-ui/).
Aim is portability and performance to enable planned p2p features.

### test

```bash
go test -v ./...
go test -v ./... -run "^SomeTest_Func$" 
```

### build

```bash
go build -ldflags "-s -w"
```

### non standart lib packages

- `github.com/urfave/cli/v3` for cli
- `github.com/rs/cors` for cors
- `gorm.io/gorm + ...` as orm for sqlite + psql and convenience
- `golang.org/x/crypto` password hashing
- ~~`github.com/alexedwards/scs/v2` for session authentication~~