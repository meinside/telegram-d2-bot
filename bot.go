package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	// telegram bot
	tg "github.com/meinside/telegram-bot-go"

	// version string
	"github.com/meinside/version-go"

	// d2
	"oss.terrastruct.com/d2/d2compiler"
	"oss.terrastruct.com/d2/d2exporter"
	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/d2target"
	"oss.terrastruct.com/d2/lib/png"
	"oss.terrastruct.com/d2/lib/textmeasure"
)

// constants
const (
	defaultMonitoringInterval = 5

	commandStart = "/start"
	commandHelp  = "/help"

	messageHelp = `This is a [Telegram Bot](https://github\.com/meinside/telegram\-d2\-bot) which replies to your messages with [D2](https://github\.com/terrastruct/d2)\-generated \.svg files in \.png format\.
	`
	messageNotSupported = "This type of message is not supported (yet)."

	renderPadding = 40
)

// struct for configuration
type config struct {
	// configs for telegram
	APIToken        string   `json:"api_token"`
	AllowedIDs      []string `json:"allowed_ids"`
	MonitorInterval int      `json:"monitor_interval"`

	// d2 rendering style
	ThemeID int64 `json:"theme_id,omitempty"` // NOTE: pick `ID` from https://github.com/terrastruct/d2/tree/master/d2themes/d2themescatalog
	Sketch  bool  `json:"sketch,omitempty"`

	// logging
	IsVerbose bool `json:"is_verbose,omitempty"`
}

// read config file
func loadConfig(filepath string) (conf config, err error) {
	if err == nil {
		var bytes []byte
		bytes, err = os.ReadFile(filepath)
		if err == nil {
			err = json.Unmarshal(bytes, &conf)
			if err == nil {
				return conf, nil
			}
		}
	}
	return config{}, err
}

// renderDiagram returns a bytes array of the rendered svg diagram in .png format.
func renderDiagram(conf config, str string) (bs []byte, err error) {
	var graph *d2graph.Graph
	if graph, err = d2compiler.Compile("", strings.NewReader(str), &d2compiler.CompileOptions{UTF16: true}); err == nil {
		var ruler *textmeasure.Ruler
		if ruler, err = textmeasure.NewRuler(); err == nil {
			if err = graph.SetDimensions(nil, ruler, nil); err == nil { // fontFamily = nil: use default
				ctx := context.Background()
				if err = d2dagrelayout.Layout(ctx, graph, nil); err == nil { // opts = nil: use default
					var diagram *d2target.Diagram
					if diagram, err = d2exporter.Export(ctx, graph, nil); err == nil { // fontFamily = nil: use default
						var out []byte
						if out, err = d2svg.Render(diagram, &d2svg.RenderOpts{
							Pad:           renderPadding,
							Sketch:        conf.Sketch,
							ThemeID:       conf.ThemeID,
							DarkThemeID:   d2svg.DEFAULT_DARK_THEME,
							SetDimensions: true,
						}); err == nil { // opts = nil: use default
							var pw png.Playwright
							if pw, err = png.InitPlaywright(); err == nil {
								defer func() {
									e := pw.Cleanup()
									if err == nil {
										err = e
									}
								}()

								if out, err = png.ConvertSVG(nil, pw.Page, out); err == nil {
									return out, nil
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

// checks if given `id` is allowed.
func checkAllowance(allowedIds []string, id *string) bool {
	if id == nil {
		return false
	}

	for _, v := range allowedIds {
		if v == *id {
			return true
		}
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
			tg.InputFileFromBytes(bs),
			tg.OptionsSendDocument{}.
				SetReplyToMessageID(messageID)); !sent.Ok {
			log.Printf("failed to send rendered image: %s", *sent.Description)
		}
	} else {
		log.Printf("failed to render message: %s", err)
	}
}

// replies to `messageId` with `text`.
func replyError(bot *tg.Bot, chatID, messageID int64, text string) {
	if sent := bot.SendMessage(
		chatID,
		text,
		tg.OptionsSendMessage{}.
			SetReplyToMessageID(messageID)); !sent.Ok {
		log.Printf("failed to send rendered image: %s", *sent.Description)
	}
}

// handles a text message
func handleMessage(bot *tg.Bot, conf config, message tg.Message) {
	username := message.From.Username

	if checkAllowance(conf.AllowedIDs, username) {
		txt := *message.Text
		chatID := message.Chat.ID
		messageID := message.MessageID

		replyRendered(bot, conf, chatID, messageID, txt)
	} else {
		if username == nil {
			log.Printf("received a message from an unauthorized user: '%s'", message.From.FirstName)
		} else {
			log.Printf("received a message from an unauthorized user: @%s", *username)
		}
	}
}

// handles a document message
func handleDocument(bot *tg.Bot, conf config, message tg.Message) {
	username := message.From.Username

	if checkAllowance(conf.AllowedIDs, username) {
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
		if username == nil {
			log.Printf("received a document from an unauthorized user: '%s'", message.From.FirstName)
		} else {
			log.Printf("received a document from an unauthorized user: @%s", *username)
		}
	}
}

// handles a non-supported message
func handleNoSupport(bot *tg.Bot, conf config, update tg.Update) {
	if from := update.GetFrom(); from != nil {
		if checkAllowance(conf.AllowedIDs, from.Username) {
			if message, _ := update.GetMessage(); message != nil {
				chatID := message.Chat.ID
				messageID := message.MessageID

				replyError(bot, chatID, messageID, messageNotSupported)
			} else {
				log.Printf("no usabale message: %s", update.String())
			}
		} else {
			if from.Username != nil {
				log.Printf("received a message from an unauthorized user: @%s", *from.Username)
			} else {
				log.Printf("received a message from an unauthorized user: '%s'", from.FirstName)
			}
		}
	}
}

// handle help command
func handleHelpCommand(b *tg.Bot, update tg.Update, args string) {
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
		client := tg.NewClient(conf.APIToken)
		client.Verbose = conf.IsVerbose

		if me := client.GetMe(); me.Ok {
			if deleted := client.DeleteWebhook(false); deleted.Ok {
				log.Printf("starting bot %s: @%s (%s)", version.Minimum(), *me.Result.Username, me.Result.FirstName)

				interval := conf.MonitorInterval
				if interval <= 0 {
					interval = defaultMonitoringInterval
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
				client.AddCommandHandler(commandStart, handleHelpCommand)
				client.AddCommandHandler(commandHelp, handleHelpCommand)

				// start polling
				client.StartMonitoringUpdates(0, interval, func(b *tg.Bot, update tg.Update, err error) {
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
