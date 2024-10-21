package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"

	// telegram bot
	tg "github.com/meinside/telegram-bot-go"

	// version string
	"github.com/meinside/version-go"

	// infisical
	infisical "github.com/infisical/go-sdk"
	"github.com/infisical/go-sdk/packages/models"

	// d2
	"oss.terrastruct.com/d2/d2compiler"
	"oss.terrastruct.com/d2/d2exporter"
	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/d2target"
	"oss.terrastruct.com/d2/lib/png"
	"oss.terrastruct.com/d2/lib/textmeasure"

	// others
	"github.com/tailscale/hujson"
)

// constants
const (
	defaultPollingInterval = 5

	commandStart   = "/start"
	commandHelp    = "/help"
	commandPrivacy = "/privacy"

	messageHelp = `This is a [Telegram Bot](https://github\.com/meinside/telegram\-d2\-bot) which replies to your messages with [D2](https://github\.com/terrastruct/d2)\-generated \.svg files in \.png format\.
`
	messagePrivacy           = `[Privacy Policy](https://github\.com/meinside/telegram\-d2\-bot/raw/master/PRIVACY\.md)`
	messageNotSupported      = "This type of message is not supported (yet)."
	messageNoMatchingCommand = "Not a supported command: %s"

	renderPadding int64 = 40
)

// struct for configuration
type config struct {
	// configurations
	AllowedIDs      []string `json:"allowed_ids"`
	MonitorInterval int      `json:"monitor_interval"`

	// d2 rendering style
	ThemeID int64 `json:"theme_id,omitempty"` // NOTE: pick `ID` from https://github.com/terrastruct/d2/tree/master/d2themes/d2themescatalog
	Sketch  bool  `json:"sketch,omitempty"`

	// logging
	IsVerbose bool `json:"is_verbose,omitempty"`

	// Bot API token
	BotToken string `json:"bot_token,omitempty"`

	// or Infisical settings
	Infisical *struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`

		ProjectID   string `json:"project_id"`
		Environment string `json:"environment"`
		SecretType  string `json:"secret_type"`

		BotTokenKeyPath string `json:"bot_token_key_path"`
	} `json:"infisical,omitempty"`
}

// read config file
func loadConfig(filepath string) (conf config, err error) {
	var bytes []byte
	if bytes, err = os.ReadFile(filepath); err == nil {
		if bytes, err = standardizeJSON(bytes); err == nil {
			if err = json.Unmarshal(bytes, &conf); err == nil {
				if conf.BotToken == "" && conf.Infisical != nil {
					// read bot token from infisical
					client := infisical.NewInfisicalClient(context.TODO(), infisical.Config{
						SiteUrl: "https://app.infisical.com",
					})

					_, err = client.Auth().UniversalAuthLogin(conf.Infisical.ClientID, conf.Infisical.ClientSecret)
					if err != nil {
						return config{}, fmt.Errorf("failed to authenticate with Infisical: %s", err)
					}

					keyPath := conf.Infisical.BotTokenKeyPath

					var secret models.Secret
					secret, err = client.Secrets().Retrieve(infisical.RetrieveSecretOptions{
						ProjectID:   conf.Infisical.ProjectID,
						Type:        conf.Infisical.SecretType,
						Environment: conf.Infisical.Environment,
						SecretPath:  path.Dir(keyPath),
						SecretKey:   path.Base(keyPath),
					})
					if err != nil {
						return config{}, fmt.Errorf("failed to retrieve telegram bot token from Infisical: %s", err)
					}

					conf.BotToken = secret.SecretValue
				}
			}
		}
	}

	return conf, err
}

// standardize given JSON (JWCC) bytes
func standardizeJSON(b []byte) ([]byte, error) {
	ast, err := hujson.Parse(b)
	if err != nil {
		return b, err
	}
	ast.Standardize()

	return ast.Pack(), nil
}

// convert any value to a pointer
func toPointer[T any](v T) *T {
	val := v
	return &val
}

// renderDiagram returns a bytes array of the rendered svg diagram in .png format.
func renderDiagram(conf config, str string) (bs []byte, err error) {
	var graph *d2graph.Graph
	if graph, _, err = d2compiler.Compile("", strings.NewReader(str), &d2compiler.CompileOptions{UTF16Pos: true}); err == nil {
		var ruler *textmeasure.Ruler
		if ruler, err = textmeasure.NewRuler(); err == nil {
			if err = graph.SetDimensions(nil, ruler, nil); err == nil { // fontFamily = nil: use default
				ctx := context.Background()
				defer ctx.Done()

				if err = d2dagrelayout.Layout(ctx, graph, nil); err == nil { // opts = nil: use default
					var diagram *d2target.Diagram
					if diagram, err = d2exporter.Export(ctx, graph, nil); err == nil { // fontFamily = nil: use default
						if bs, err = d2svg.Render(diagram, &d2svg.RenderOpts{
							Pad:         toPointer(renderPadding),
							Sketch:      toPointer(conf.Sketch),
							ThemeID:     toPointer(conf.ThemeID),
							DarkThemeID: d2svg.DEFAULT_DARK_THEME,
							Scale:       toPointer(1.0), // 1:1
						}); err == nil { // opts = nil: use default
							var pw png.Playwright
							if pw, err = png.InitPlaywright(); err == nil {
								defer func() {
									e := pw.Cleanup()
									if err == nil {
										err = e
									}
								}()

								if bs, err = png.ConvertSVG(pw.Page, bs); err == nil {
									return bs, nil
								}
							}
						}
					}
				}
			}
		}
	}
	return nil, err
}

// checks if given username is allowed.
func isUsernameAllowed(conf config, username *string) bool {
	if username == nil {
		return false
	}

	for _, v := range conf.AllowedIDs {
		if v == *username {
			return true
		}
	}

	return false
}

// checks if given update is allowed.
func isUpdateAllowed(conf config, update tg.Update) bool {
	if from := update.GetFrom(); from != nil {
		return isUsernameAllowed(conf, from.Username)
	}

	return false
}

// renders a .png file with given `text` and reply to `messageId` with it.
func replyRendered(bot *tg.Bot, conf config, chatID, messageID int64, text string) {
	// typing...
	_ = bot.SendChatAction(chatID, tg.ChatActionTyping, nil)

	// render text into .svg and convert it to .png bytes
	if bs, err := renderDiagram(conf, text); err == nil {
		if sent := bot.SendDocument(
			chatID,
			tg.NewInputFileFromBytes(bs),
			tg.OptionsSendDocument{}.
				SetReplyParameters(tg.NewReplyParameters(messageID))); !sent.Ok {
			log.Printf("failed to send rendered image: %s", *sent.Description)
		} else {
			if reactioned := bot.SetMessageReaction(chatID, messageID, tg.NewMessageReactionWithEmoji("👌")); !reactioned.Ok {
				log.Printf("failed to set reaction: %s", *reactioned.Description)
			}
		}
	} else {
		log.Printf("failed to render message: %s", err)

		replyError(bot, chatID, messageID, fmt.Sprintf("Failed to render message: %s", err))
	}
}

// replies to `messageId` with `text`.
func replyError(bot *tg.Bot, chatID, messageID int64, text string) {
	if sent := bot.SendMessage(
		chatID,
		text,
		tg.OptionsSendMessage{}.
			SetReplyParameters(tg.NewReplyParameters(messageID))); !sent.Ok {
		log.Printf("failed to send rendered image: %s", *sent.Description)
	}
}

// handles a text message
func handleMessage(bot *tg.Bot, conf config, message tg.Message) {
	username := message.From.Username

	if isUsernameAllowed(conf, username) {
		txt := *message.Text
		chatID := message.Chat.ID
		messageID := message.MessageID

		replyRendered(bot, conf, chatID, messageID, txt)
	} else {
		if conf.IsVerbose {
			log.Printf("message not allowed: %+v", message)
		}
	}
}

// handles a document message
func handleDocument(bot *tg.Bot, conf config, message tg.Message) {
	username := message.From.Username

	if isUsernameAllowed(conf, username) {
		document := *message.Document
		chatID := message.Chat.ID
		messageID := message.MessageID

		if document.FileName != nil && strings.HasSuffix(*document.FileName, ".d2") {
			if file := bot.GetFile(document.FileID); file.Ok {
				url := bot.GetFileURL(*file.Result)
				if content, err := getURL(url); err == nil {
					message := string(content)

					replyRendered(bot, conf, chatID, messageID, message)
				} else {
					log.Printf("failed to fetch '%s': %s", url, err)
				}
			} else {
				log.Printf("failed to fetch file with id: %s", document.FileID)
			}
		} else {
			if document.FileName != nil {
				replyError(bot, chatID, messageID, fmt.Sprintf("'%s' does not seem to be a .d2 file.", *document.FileName))
			}
		}
	} else {
		if conf.IsVerbose {
			log.Printf("document not allowed: %+v", message)
		}
	}
}

// handles a non-supported message
func handleNoSupport(bot *tg.Bot, conf config, update tg.Update) {
	if isUpdateAllowed(conf, update) {
		if message, _ := update.GetMessage(); message != nil {
			chatID := message.Chat.ID
			messageID := message.MessageID

			replyError(bot, chatID, messageID, messageNotSupported)
		} else {
			log.Printf("no usabale message: %+v", update)
		}
	} else {
		if conf.IsVerbose {
			log.Printf("update not allowed: %+v", update)
		}
	}
}

// handle help command
func handleHelpCommand(b *tg.Bot, conf config, update tg.Update) {
	if isUpdateAllowed(conf, update) {
		if message, _ := update.GetMessage(); message != nil {
			chatID := message.Chat.ID

			if sent := b.SendMessage(
				chatID,
				messageHelp,
				tg.OptionsSendMessage{}.
					SetParseMode(tg.ParseModeMarkdownV2)); !sent.Ok {
				log.Printf("failed to send help message: %s", *sent.Description)
			}
		}
	} else {
		if conf.IsVerbose {
			log.Printf("update not allowed: %+v", update)
		}
	}
}

// handle privacy command
func handlePrivacyCommand(b *tg.Bot, update tg.Update) {
	if message, _ := update.GetMessage(); message != nil {
		chatID := message.Chat.ID

		if sent := b.SendMessage(
			chatID,
			messagePrivacy,
			tg.OptionsSendMessage{}.
				SetParseMode(tg.ParseModeMarkdownV2)); !sent.Ok {
			log.Printf("failed to send privacy policy: %s", *sent.Description)
		}
	}
}

// handle no matching command
func handleNoMatchingCommand(b *tg.Bot, conf config, update tg.Update, cmd string) {
	if isUpdateAllowed(conf, update) {
		if message, _ := update.GetMessage(); message != nil {
			chatID := message.Chat.ID

			if sent := b.SendMessage(
				chatID,
				fmt.Sprintf(messageNoMatchingCommand, cmd),
				tg.OptionsSendMessage{}.
					SetParseMode(tg.ParseModeMarkdownV2)); !sent.Ok {
				log.Printf("failed to send no-matching-command message: %s", *sent.Description)
			}
		}
	} else {
		if conf.IsVerbose {
			log.Printf("update not allowed: %+v", update)
		}
	}
}

// get file bytes from given url
func getURL(url string) (content []byte, err error) {
	var res *http.Response
	if res, err = http.Get(url); err != nil {
		return nil, err
	}

	defer res.Body.Close()

	content, err = io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	return content, nil
}

// runs the bot with config file's path
func runBot(confFilepath string) {
	if conf, err := loadConfig(confFilepath); err != nil {
		panic(err)
	} else {
		client := tg.NewClient(conf.BotToken)
		client.Verbose = conf.IsVerbose

		if me := client.GetMe(); me.Ok {
			if deleted := client.DeleteWebhook(false); deleted.Ok {
				log.Printf("starting bot %s: @%s (%s)", version.Minimum(), *me.Result.Username, me.Result.FirstName)

				interval := conf.MonitorInterval
				if interval <= 0 {
					interval = defaultPollingInterval
				}

				// set update handlers
				client.SetMessageHandler(func(b *tg.Bot, update tg.Update, message tg.Message, edited bool) {
					if message.HasText() {
						handleMessage(b, conf, message)
					} else if message.HasDocument() {
						handleDocument(b, conf, message)
					}
				})

				// set command handlers
				client.AddCommandHandler(commandStart, func(b *tg.Bot, update tg.Update, args string) {
					handleHelpCommand(b, conf, update)
				})
				client.AddCommandHandler(commandHelp, func(b *tg.Bot, update tg.Update, args string) {
					handleHelpCommand(b, conf, update)
				})
				client.AddCommandHandler(commandPrivacy, func(b *tg.Bot, update tg.Update, args string) {
					handlePrivacyCommand(b, update)
				})
				client.SetNoMatchingCommandHandler(func(b *tg.Bot, update tg.Update, cmd, args string) {
					handleNoMatchingCommand(b, conf, update, cmd)
				})

				// start polling
				client.StartPollingUpdates(0, interval, func(b *tg.Bot, update tg.Update, err error) {
					if err != nil {
						log.Printf("failed to poll updates: %s", err.Error())
					} else {
						// do nothing (messages are handled by specified update handler)
						handleNoSupport(b, conf, update)
					}
				})
			} else {
				log.Printf("failed to delete webhook: %s", *deleted.Description)
			}
		} else {
			log.Printf("failed to get bot information: %s", *me.Description)
		}
	}
}
