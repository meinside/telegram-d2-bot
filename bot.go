package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

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
	defaultPollingInterval = 5

	requestTimeoutSeconds          = 30
	longRequestTimeoutSeconds      = 60
	ignorableRequestTimeoutSeconds = 3

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

// convert any value to a pointer
func toPointer[T any](v T) *T {
	val := v
	return &val
}

// renderDiagram returns a bytes array of the rendered svg diagram in .png format.
func renderDiagram(
	ctx context.Context,
	conf config,
	str string,
) (bs []byte, err error) {
	ctxRender, cancelRender := context.WithTimeout(ctx, longRequestTimeoutSeconds*time.Second)
	defer cancelRender()

	var graph *d2graph.Graph
	if graph, _, err = d2compiler.Compile(
		"",
		strings.NewReader(str),
		&d2compiler.CompileOptions{UTF16Pos: true},
	); err == nil {
		var ruler *textmeasure.Ruler
		if ruler, err = textmeasure.NewRuler(); err == nil {
			if err = graph.SetDimensions(
				nil,
				ruler,
				nil, // NOTE: use default
				nil, // NOTE: use default
			); err == nil {
				if err = d2dagrelayout.Layout(
					ctxRender,
					graph,
					nil, // NOTE: use default
				); err == nil {
					var diagram *d2target.Diagram
					if diagram, err = d2exporter.Export(
						ctxRender,
						graph,
						nil, // NOTE: use default
						nil, // NOTE: use default
					); err == nil {
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
func isUsernameAllowed(
	conf config,
	username *string,
) bool {
	if username == nil {
		return false
	}

	return slices.Contains(conf.AllowedIDs, *username)
}

// checks if given update is allowed.
func isUpdateAllowed(
	conf config,
	update tg.Update,
) bool {
	if from := update.GetFrom(); from != nil {
		return isUsernameAllowed(conf, from.Username)
	}

	return false
}

// renders a .png file with given `text` and reply to `messageId` with it.
func replyRendered(
	ctx context.Context,
	bot *tg.Bot,
	conf config,
	chatID, messageID int64,
	text string,
) {
	// typing...
	ctxAction, cancelAction := context.WithTimeout(ctx, ignorableRequestTimeoutSeconds*time.Second)
	defer cancelAction()
	_, _ = bot.SendChatAction(ctxAction, chatID, tg.ChatActionTyping, nil)

	// render text into .svg and convert it to .png bytes
	if bs, err := renderDiagram(ctx, conf, text); err == nil {
		// send document
		ctxSend, cancelSend := context.WithTimeout(ctx, requestTimeoutSeconds*time.Second)
		defer cancelSend()
		if sent, _ := bot.SendDocument(
			ctxSend,
			chatID,
			tg.NewInputFileFromBytes(bs),
			tg.OptionsSendDocument{}.
				SetReplyParameters(tg.NewReplyParameters(messageID)),
		); !sent.OK {
			log.Printf("failed to send rendered image: %s", *sent.Description)
		} else {
			// add reaction
			ctxReaction, cancelReaction := context.WithTimeout(ctx, requestTimeoutSeconds*time.Second)
			defer cancelReaction()
			if reactioned, _ := bot.SetMessageReaction(
				ctxReaction,
				chatID,
				messageID,
				tg.NewMessageReactionWithEmoji("👌"),
			); !reactioned.OK {
				log.Printf("failed to set reaction: %s", *reactioned.Description)
			}
		}
	} else {
		log.Printf("failed to render message: %s", err)

		replyError(
			ctx,
			bot,
			chatID,
			messageID,
			fmt.Sprintf("Failed to render message: %s", err),
		)
	}
}

// replies to `messageId` with `text`.
func replyError(
	ctx context.Context,
	bot *tg.Bot,
	chatID, messageID int64,
	text string,
) {
	ctxSend, cancelSend := context.WithTimeout(ctx, requestTimeoutSeconds*time.Second)
	defer cancelSend()
	if sent, _ := bot.SendMessage(
		ctxSend,
		chatID,
		text,
		tg.OptionsSendMessage{}.
			SetReplyParameters(tg.NewReplyParameters(messageID))); !sent.OK {
		log.Printf("failed to send rendered image: %s", *sent.Description)
	}
}

// handles a text message
func handleMessage(
	ctx context.Context,
	bot *tg.Bot,
	conf config,
	message tg.Message,
) {
	username := message.From.Username

	if isUsernameAllowed(conf, username) {
		txt := *message.Text
		chatID := message.Chat.ID
		messageID := message.MessageID

		replyRendered(
			ctx,
			bot,
			conf,
			chatID,
			messageID,
			txt,
		)
	} else {
		if conf.IsVerbose {
			log.Printf("message not allowed: %+v", message)
		}
	}
}

// handles a document message
func handleDocument(
	ctx context.Context,
	bot *tg.Bot,
	conf config,
	message tg.Message,
) {
	username := message.From.Username

	if isUsernameAllowed(conf, username) {
		document := *message.Document
		chatID := message.Chat.ID
		messageID := message.MessageID

		if document.FileName != nil && strings.HasSuffix(*document.FileName, ".d2") {
			// get file info
			ctxFile, cancelFile := context.WithTimeout(ctx, requestTimeoutSeconds*time.Second)
			defer cancelFile()
			if file, _ := bot.GetFile(ctxFile, document.FileID); file.OK {
				url := bot.GetFileURL(*file.Result)
				if content, err := getBytesFromURL(ctx, url); err == nil {
					message := string(content)

					replyRendered(ctx, bot, conf, chatID, messageID, message)
				} else {
					log.Printf("failed to fetch '%s': %s", url, err)
				}
			} else {
				log.Printf("failed to fetch file with id: %s", document.FileID)
			}
		} else {
			if document.FileName != nil {
				replyError(
					ctx,
					bot,
					chatID,
					messageID,
					fmt.Sprintf("'%s' does not seem to be a .d2 file.", *document.FileName),
				)
			}
		}
	} else {
		if conf.IsVerbose {
			log.Printf("document not allowed: %+v", message)
		}
	}
}

// handles a non-supported message
func handleNoSupport(
	ctx context.Context,
	bot *tg.Bot,
	conf config,
	update tg.Update,
) {
	if isUpdateAllowed(conf, update) {
		if message, _ := update.GetMessage(); message != nil {
			chatID := message.Chat.ID
			messageID := message.MessageID

			replyError(
				ctx,
				bot,
				chatID,
				messageID,
				messageNotSupported,
			)
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
func handleHelpCommand(
	ctx context.Context,
	b *tg.Bot,
	conf config,
	update tg.Update,
) {
	if isUpdateAllowed(conf, update) {
		if message, _ := update.GetMessage(); message != nil {
			chatID := message.Chat.ID

			ctxSend, cancelSend := context.WithTimeout(ctx, requestTimeoutSeconds*time.Second)
			defer cancelSend()
			if sent, _ := b.SendMessage(
				ctxSend,
				chatID,
				messageHelp,
				tg.OptionsSendMessage{}.
					SetParseMode(tg.ParseModeMarkdownV2)); !sent.OK {
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
func handlePrivacyCommand(
	ctx context.Context,
	b *tg.Bot,
	update tg.Update,
) {
	if message, _ := update.GetMessage(); message != nil {
		chatID := message.Chat.ID

		ctxSend, cancelSend := context.WithTimeout(ctx, requestTimeoutSeconds*time.Second)
		defer cancelSend()
		if sent, _ := b.SendMessage(
			ctxSend,
			chatID,
			messagePrivacy,
			tg.OptionsSendMessage{}.
				SetParseMode(tg.ParseModeMarkdownV2)); !sent.OK {
			log.Printf("failed to send privacy policy: %s", *sent.Description)
		}
	}
}

// handle no matching command
func handleNoMatchingCommand(
	ctx context.Context,
	b *tg.Bot,
	conf config,
	update tg.Update,
	cmd string,
) {
	if isUpdateAllowed(conf, update) {
		if message, _ := update.GetMessage(); message != nil {
			chatID := message.Chat.ID

			ctxSend, cancelSend := context.WithTimeout(ctx, requestTimeoutSeconds*time.Second)
			defer cancelSend()
			if sent, _ := b.SendMessage(
				ctxSend,
				chatID,
				fmt.Sprintf(messageNoMatchingCommand, cmd),
				tg.OptionsSendMessage{}.
					SetParseMode(tg.ParseModeMarkdownV2)); !sent.OK {
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
func getBytesFromURL(
	ctx context.Context,
	url string,
) (content []byte, err error) {
	ctxBytes, cancelBytes := context.WithTimeout(ctx, longRequestTimeoutSeconds*time.Second)
	defer cancelBytes()

	// create request
	var req *http.Request
	if req, err = http.NewRequestWithContext(ctxBytes, http.MethodGet, url, nil); err != nil {
		return nil, err
	}

	// send request
	var res *http.Response
	if res, err = http.DefaultClient.Do(req); err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()

	// read bytes from response
	content, err = io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	return content, nil
}

// runs the bot with config file's path
func runBot(confFilepath string) {
	ctx := context.Background()

	if conf, err := loadConfig(ctx, confFilepath); err != nil {
		panic(err)
	} else {
		client := tg.NewClient(conf.BotToken)
		client.Verbose = conf.IsVerbose

		// get bot info
		ctxBotInfo, cancelBotInfo := context.WithTimeout(ctx, requestTimeoutSeconds*time.Second)
		defer cancelBotInfo()
		if me, _ := client.GetMe(ctxBotInfo); me.OK {
			// delete webhook before polling updates
			ctxDeleteWebhook, cancelDeleteWebhook := context.WithTimeout(ctx, requestTimeoutSeconds*time.Second)
			defer cancelDeleteWebhook()
			if deleted, _ := client.DeleteWebhook(ctxDeleteWebhook, false); deleted.OK {
				log.Printf("starting bot %s: @%s (%s)", version.Minimum(), *me.Result.Username, me.Result.FirstName)

				interval := conf.MonitorInterval
				if interval <= 0 {
					interval = defaultPollingInterval
				}

				// set update handlers
				client.SetMessageHandler(func(
					b *tg.Bot,
					update tg.Update,
					message tg.Message,
					edited bool,
				) {
					if message.HasText() {
						handleMessage(ctx, b, conf, message)
					} else if message.HasDocument() {
						handleDocument(ctx, b, conf, message)
					}
				})

				// set command handlers
				client.AddCommandHandler(commandStart, func(
					b *tg.Bot,
					update tg.Update,
					args string,
				) {
					handleHelpCommand(ctx, b, conf, update)
				})
				client.AddCommandHandler(commandHelp, func(
					b *tg.Bot,
					update tg.Update,
					args string,
				) {
					handleHelpCommand(ctx, b, conf, update)
				})
				client.AddCommandHandler(commandPrivacy, func(
					b *tg.Bot,
					update tg.Update,
					args string,
				) {
					handlePrivacyCommand(ctx, b, update)
				})
				client.SetNoMatchingCommandHandler(func(
					b *tg.Bot,
					update tg.Update,
					cmd, args string,
				) {
					handleNoMatchingCommand(ctx, b, conf, update, cmd)
				})

				// start polling
				client.StartPollingUpdates(
					0,
					interval,
					func(b *tg.Bot, update tg.Update, err error) {
						if err != nil {
							log.Printf("failed to poll updates: %s", err.Error())
						} else {
							// do nothing (messages are handled by specified update handler)
							handleNoSupport(ctx, b, conf, update)
						}
					},
				)
			} else {
				log.Printf("failed to delete webhook: %s", *deleted.Description)
			}
		} else {
			log.Printf("failed to get bot information: %s", *me.Description)
		}
	}
}
