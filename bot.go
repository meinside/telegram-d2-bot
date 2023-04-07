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

const (
	defaultMonitoringInterval = 5

	commandStart = "/start"
	messageStart = `This is a [Telegram Bot](https://github\.com/meinside/telegram\-d2\-bot) which replies to your messages with [D2](https://github\.com/terrastruct/d2)\-generated \.svg files in \.png format\.
	`

	renderPadding = 40
)

type config struct {
	APIToken        string   `json:"api_token"`
	AllowedIds      []string `json:"allowed_ids"`
	MonitorInterval int      `json:"monitor_interval"`
	Sketch          bool     `json:"sketch,omitempty"`
	IsVerbose       bool     `json:"is_verbose,omitempty"`
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
func renderDiagram(str string, sketch bool) (bs []byte, err error) {
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
							Sketch:        sketch,
							ThemeID:       d2svg.DEFAULT_THEME,
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
func replyRendered(bot *tg.Bot, chatID, messageID int64, text string, sketch bool) {
	// typing...
	_ = bot.SendChatAction(chatID, tg.ChatActionTyping, nil)

	// render text into .svg and convert it to .png bytes
	if bs, err := renderDiagram(text, sketch); err == nil {
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
func handleMessage(bot *tg.Bot, message tg.Message, allowedIds []string, sketch bool) {
	username := message.From.Username

	if checkAllowance(allowedIds, username) {
		txt := *message.Text
		chatID := message.Chat.ID

		if strings.HasPrefix(txt, "/") { // handle commands here
			switch txt {
			case commandStart:
				if sent := bot.SendMessage(
					chatID,
					messageStart,
					tg.OptionsSendMessage{}.
						SetParseMode(tg.ParseModeMarkdownV2)); !sent.Ok {
					log.Printf("failed to send start message: %s", *sent.Description)
				}
			}

			// unhandled commands reach here
		} else { // handle non-commands here
			messageID := message.MessageID

			replyRendered(bot, chatID, messageID, txt, sketch)
		}
	} else {
		if username == nil {
			log.Printf("received a message from an unauthorized user: '%s'", message.From.FirstName)
		} else {
			log.Printf("received a message from an unauthorized user: @%s", *username)
		}
	}
}

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

// handles a document message
func handleDocument(bot *tg.Bot, update tg.Update, allowedIds []string, sketch bool) {
	username := update.Message.From.Username

	if checkAllowance(allowedIds, username) {
		document := *update.Message.Document
		chatID := update.Message.Chat.ID
		messageID := update.Message.MessageID

		if document.FileName != nil && strings.HasSuffix(*document.FileName, ".d2") {
			if file := bot.GetFile(document.FileID); file.Ok {
				url := bot.GetFileURL(*file.Result)
				if content, err := getURL(url); err == nil {
					message := string(content)

					replyRendered(bot, chatID, messageID, message, sketch)
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
			log.Printf("received a document from an unauthorized user: '%s'", update.Message.From.FirstName)
		} else {
			log.Printf("received a document from an unauthorized user: @%s", *username)
		}
	}
}

// generates a function for handling updates
func updateHandleFunc(allowedIds []string, sketch bool) func(*tg.Bot, tg.Update, error) {
	return func(bot *tg.Bot, update tg.Update, err error) {
		if err != nil {
			log.Printf("failed to fetch update: %s", err)
			return
		}

		if update.HasMessage() && update.Message.HasText() {
			handleMessage(bot, *update.Message, allowedIds, sketch)
		} else if update.HasEditedMessage() && update.EditedMessage.HasText() {
			handleMessage(bot, *update.EditedMessage, allowedIds, sketch)
		} else if update.Message.HasDocument() {
			handleDocument(bot, update, allowedIds, sketch)
		}
	}
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
				log.Printf("starting bot: @%s (%s)", *me.Result.Username, me.Result.FirstName)

				interval := conf.MonitorInterval
				if interval <= 0 {
					interval = defaultMonitoringInterval
				}

				client.StartMonitoringUpdates(0, interval, updateHandleFunc(conf.AllowedIds, conf.Sketch))
			} else {
				log.Printf("failed to delete webhook: %s", *deleted.Description)
			}
		} else {
			log.Printf("failed to get bot information: %s", *me.Description)
		}
	}
}