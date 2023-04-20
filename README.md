# telegram-d2-bot

A telegram bot which answers with rendered `.svg` files in `.png` format.

Using [terrastruct/d2](https://github.com/terrastruct/d2) for generating .svg files from messages.

<img width="631" alt="Screenshot 2022-12-19 at 14 31 53" src="https://user-images.githubusercontent.com/185988/208354666-fe073dbc-105a-44b3-88a0-dce64a454efc.png">

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
    "theme_id": 0,
    "sketch": false,
    "is_verbose": false
}
```

* `api_token` can be obtained from [bot father](https://t.me/botfather)
* `allowed_ids` are ids of allowed telegram users who can get responses from this bot
* `monitor_interval` is the polling interval (in seconds) from telegram API
* `theme_id` can be retrieved from [these files](https://github.com/terrastruct/d2/tree/master/d2themes/d2themescatalog) (= 0 for default)
* `sketch` is whether to render results in sketched style
* `is_verbose` is whether to print verbose messages

## Other Dependencies

[Playwright](https://github.com/playwright-community/playwright-go) is needed for exporting .png files:

```bash
$ npx playwright install-deps
```

## Build and Run

```bash
$ go build
$ ./telegram-d2-bot config.json
```

## Run as a service

### systemd

Create a file named `/lib/systemd/system/telegram-d2-bot.service`:

```
[Unit]
Description=Telegram D2 Bot
After=syslog.target
After=network.target

[Service]
Type=simple
User=ubuntu
Group=ubuntu
WorkingDirectory=/dir/to/telegram-d2-bot
ExecStart=/dir/to/telegram-d2-bot/telegram-d2-bot [CONFIG_FILEPATH]
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

and make it run automatically on booting:

```bash
$ sudo systemctl enable telegram-d2-bot.service
$ sudo systemctl start telegram-d2-bot.service
```

## Todos

- [ ] Add more configurable options.
- [x] Support uploading .d2 files.
- [x] Respond with .png files. (Playwright is needed)

