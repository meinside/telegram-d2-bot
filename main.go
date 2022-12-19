package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	// d2
	"oss.terrastruct.com/d2/d2compiler"
	"oss.terrastruct.com/d2/d2exporter"
	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/d2target"
	"oss.terrastruct.com/d2/d2themes/d2themescatalog"
	"oss.terrastruct.com/d2/lib/png"
	"oss.terrastruct.com/d2/lib/textmeasure"

	// telegram bot
	tg "github.com/meinside/telegram-bot-go"
	"github.com/playwright-community/playwright-go"
)

const (
	defaultMonitoringInterval = 5

	commandStart = "/start"
	messageStart = `This is a [Telegram Bot](https://github\.com/meinside/telegram\-d2\-bot) which replies to your messages with [D2](https://github\.com/terrastruct/d2)\-generated \.svg files in \.png format\.
	`
	messageUsage = `Usage:

	$ %s [CONFIG_FILE_PATH]
`
)

type config struct {
	APIToken        string   `json:"api_token"`
	AllowedIds      []string `json:"allowed_ids"`
	MonitorInterval int      `json:"monitor_interval"`
	IsVerbose       bool     `json:"is_verbose,omitempty"`
}

// read config file
func openConfig(filepath string) (conf config, err error) {
	if err == nil {
		var bytes []byte
		bytes, err = ioutil.ReadFile(filepath)
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
func renderDiagram(str string) (bs []byte, err error) {
	var graph *d2graph.Graph
	if graph, err = d2compiler.Compile("", strings.NewReader(str), &d2compiler.CompileOptions{UTF16: true}); err == nil {
		var ruler *textmeasure.Ruler
		if ruler, err = textmeasure.NewRuler(); err == nil {
			if err = graph.SetDimensions(nil, ruler); err == nil {
				ctx := context.Background()
				if err = d2dagrelayout.Layout(ctx, graph); err == nil {
					var diagram *d2target.Diagram
					if diagram, err = d2exporter.Export(ctx, graph, d2themescatalog.NeutralDefault.ID); err == nil {
						var out []byte
						if out, err = d2svg.Render(diagram); err == nil {
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
func replyRendered(bot *tg.Bot, chatID, messageID int64, text string) {
	// typing...
	_ = bot.SendChatAction(chatID, tg.ChatActionTyping)

	// render text into .svg and convert it to .png bytes
	if bs, err := renderDiagram(text); err == nil {
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
func handleMessage(bot *tg.Bot, update tg.Update, allowedIds []string) {
	username := update.Message.From.Username

	if checkAllowance(allowedIds, username) {
		message := *update.Message.Text
		chatID := update.Message.Chat.ID

		if strings.HasPrefix(message, "/") { // handle commands here
			switch message {
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
			messageID := update.Message.MessageID

			replyRendered(bot, chatID, messageID, message)
		}
	} else {
		if username == nil {
			log.Printf("received a message from an unauthorized user: '%s'", update.Message.From.FirstName)
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

	content, err = ioutil.ReadAll(res.Body)
	defer res.Body.Close()
	if err != nil {
		return nil, err
	}

	return content, nil
}

// handles a document message
func handleDocument(bot *tg.Bot, update tg.Update, allowedIds []string) {
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

					replyRendered(bot, chatID, messageID, message)
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
func updateHandleFunc(allowedIds []string) func(*tg.Bot, tg.Update, error) {
	return func(bot *tg.Bot, update tg.Update, err error) {
		if err != nil {
			log.Printf("failed to fetch update: %s", err)
			return
		}

		if update.HasMessage() && update.Message.HasText() {
			handleMessage(bot, update, allowedIds)
		} else if update.Message.HasDocument() {
			handleDocument(bot, update, allowedIds)
		}
	}
}

// runs the bot with config file's path
func runBot(confFilepath string) {
	if conf, err := openConfig(confFilepath); err != nil {
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

				client.StartMonitoringUpdates(0, interval, updateHandleFunc(conf.AllowedIds))
			} else {
				log.Printf("failed to delete webhook: %s", *deleted.Description)
			}
		} else {
			log.Printf("failed to get bot information: %s", *me.Description)
		}
	}
}

// prints usage text to standard out
func printUsage(progName string) {
	fmt.Printf(messageUsage, progName)
}

func main() {
	// install playwright browsers
	if err := playwright.Install(); err != nil {
		log.Printf("failed to install playwright browsers: %s", err)
		return
	}

	if len(os.Args) > 1 {
		runBot(os.Args[1])
	} else {
		printUsage(os.Args[0])
	}
}
