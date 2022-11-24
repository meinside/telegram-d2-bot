package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	// d2
	"oss.terrastruct.com/d2/d2compiler"
	"oss.terrastruct.com/d2/d2exporter"
	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/d2renderers/textmeasure"
	"oss.terrastruct.com/d2/d2target"
	"oss.terrastruct.com/d2/d2themes/d2themescatalog"

	// telegram bot
	tg "github.com/meinside/telegram-bot-go"
)

const (
	defaultMonitoringInterval = 5

	commandStart = "/start"
	messageStart = `This is a [Telegram Bot](https://github\.com/meinside/telegram\-d2\-bot) which replies to your messages with [D2](https://github\.com/terrastruct/d2)\-generated \.svg files\.
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

// renderDiagram returns a bytes array of the rendered svg diagram.
func renderDiagram(str string) (bs []byte, err error) {
	var graph *d2graph.Graph
	if graph, err = d2compiler.Compile("", strings.NewReader(str), &d2compiler.CompileOptions{UTF16: true}); err == nil {
		var ruler *textmeasure.Ruler
		if ruler, err = textmeasure.NewRuler(); err == nil {
			if err = graph.SetDimensions(nil, ruler); err == nil {
				ctx := context.Background()
				if err = d2dagrelayout.Layout(ctx, graph); err == nil {
					var diagram *d2target.Diagram
					// TODO: export/render to a .png file (not in .svg format)
					if diagram, err = d2exporter.Export(ctx, graph, d2themescatalog.NeutralDefault.ID); err == nil {
						var out []byte
						if out, err = d2svg.Render(diagram); err == nil {
							return out, nil
						}
					}
				}
			}
		}
	}
	return nil, err
}

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

// generates a function for handling updates
func updateHandleFunc(allowedIds []string) func(*tg.Bot, tg.Update, error) {
	return func(bot *tg.Bot, update tg.Update, err error) {
		if err == nil && update.HasMessage() && update.Message.HasText() {
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
					// typing...
					_ = bot.SendChatAction(chatID, tg.ChatActionTyping)

					// render svg into bytes
					if bs, err := renderDiagram(message); err == nil {
						messageID := update.Message.MessageID
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
			} else {
				if username == nil {
					log.Printf("received an update from an unauthorized user: '%s'", update.Message.From.FirstName)
				} else {
					log.Printf("received an update from an unauthorized user: @%s", *username)
				}
			}
		} else {
			log.Printf("failed to fetch update: %s", err)
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
	if len(os.Args) > 1 {
		runBot(os.Args[1])
	} else {
		printUsage(os.Args[0])
	}
}
