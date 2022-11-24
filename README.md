# telegram-d2-bot

A telegram bot which answers with rendered .svg files.

Using [terrastruct/d2](https://github.com/terrastruct/d2) for generating .svg files from messages.

## Configuration

```bash
$ cp config.json.sample config.json
```

and edit:

```json
{
    "api_token": "xxxxxxxxyyyyyyyy-1234567",
    "allowed_ids": ["telegram_username_1", "telegram_username_2"],
    "monitor_interval": 5,
    "is_verbose": false
}
```

## Build and Run

```bash
$ go build
$ ./telegram-d2-bot config.json
```

## Todos

- [ ] Send image files, not just xml(svg) files.

