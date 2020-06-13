package main

import (
	"fmt"
	"log"
	"miranda-bot/callbacks"
	"miranda-bot/config"
	"net/http"
	"os"
	"strconv"
	"strings"

	"miranda-bot/commands"
	"miranda-bot/models"

	"github.com/getsentry/sentry-go"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/postgres"
	"github.com/joho/godotenv"

	tg "gopkg.in/telegram-bot-api.v4"
)

// App main app struct
type App struct {
	DB     *gorm.DB
	Bot    *tg.BotAPI
	Config *config.Configuration
}

func main() {
	// Load Configuration
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file, reading from system env")
		// panic(err)
	}

	// Init Sentry
	serr := sentry.Init(sentry.ClientOptions{
		Dsn: "https://f2128fc9c33d4bfea0b33e220166a89e:e8ac6687004a476886ace7e3dcf0dd8e@sentry.io/1419349",
	})

	if serr != nil {
		log.Println("Error initialize sentry")
	}

	// Init Configuration
	groupID, _ := strconv.ParseInt(os.Getenv("GROUP_ID"), 10, 64)
	config := &config.Configuration{
		Port:        os.Getenv("PORT"),
		UpdateMode:  os.Getenv("UPDATE_MODE"),
		Token:       os.Getenv("TOKEN"),
		WebhookURL:  os.Getenv("WEBHOOK_URL"),
		DBUrl:       os.Getenv("DATABASE_URL"),
		GroupID:     groupID,
		BotUsername: os.Getenv("BOT_USERNAME"),
	}

	bot, err := tg.NewBotAPI(config.Token)

	if err != nil {
		log.Panic(err)
	}

	// Init Database
	db, err := gorm.Open("postgres", config.DBUrl)
	if err != nil {
		log.Panic("Unable connect to database", err)
	}

	defer db.Close()

	log.Println("Connected to DB")
	log.Printf("@%s working on group %v", config.BotUsername, config.GroupID)
	db.AutoMigrate(&models.User{}, &models.Report{}, &models.UserReport{})

	app := App{
		DB:     db,
		Config: config,
		Bot:    bot,
	}

	bot.Debug = false
	log.Printf("@%s is wake up.. :)", bot.Self.UserName)

	// Using Long Pooling
	if config.UpdateMode == "1" {
		log.Println("Set mode pooling & remove webhook")

		// Remove webhook if exist
		_, err := bot.RemoveWebhook()

		if err != nil {
			log.Fatal("Error removing webhook")
		}

		u := tg.NewUpdate(0)
		u.Timeout = 60

		updates, err := bot.GetUpdatesChan(u)

		if err != nil {
			log.Fatal("Error geting updates", err)
		}

		app.handleUpdates(updates)
	}

	// Using Webhook
	if config.UpdateMode == "2" {
		log.Println("Set mode webhook to", config.WebhookURL)
		_, err := bot.SetWebhook(tg.NewWebhook(config.WebhookURL))

		if err != nil {
			log.Fatal("Error setting webhook", err)
		}

		info, err := bot.GetWebhookInfo()
		if err != nil {
			log.Fatal("Error getting webhook info", err)
		}

		if info.LastErrorDate != 0 {
			log.Printf("[Telegram callback failed]%s", info.LastErrorMessage)
		}

		updates := bot.ListenForWebhook("/webhook")

		log.Println("Running on port:", config.Port)
		go http.ListenAndServe(":"+config.Port, nil)

		app.handleUpdates(updates)

	}

}

func (app *App) handleUpdates(updates tg.UpdatesChannel) {

	bot := app.Bot
	for update := range updates {
		// DEBUG INCOMING MESSAGE
		// data, _ := json.Marshal(update)
		// message := bytes.NewBufferString(string(data))

		// log.Println(message)

		if update.CallbackQuery != nil {

			cb := callbacks.Callback{
				Bot:           bot,
				CallbackQuery: update.CallbackQuery,
				DB:            app.DB,
				Config:        app.Config,
			}

			cq := update.CallbackQuery.Data

			data := strings.Split(cq, ":")

			cb.Handle(data[0])

			continue
		} else if update.Message == nil {
			continue
		}

		log.Printf("[%s:%s] %s", update.Message.From.UserName, update.Message.Chat.Title, update.Message.Text)

		switch {

		// New Member Join
		case update.Message.NewChatMembers != nil:
			//TODO: Handle welcome message
			// log.Println("New Chat Members")

			members := update.Message.NewChatMembers
			// firstMember := (*members)[0]

			// var member tg.User
			for _, member := range *members {

				if member.UserName == app.Config.BotUsername && update.Message.Chat.ID != app.Config.GroupID {
					// Left Chat on unregistered group
					_, err := bot.LeaveChat(tg.ChatConfig{
						ChatID: update.Message.Chat.ID,
					})

					log.Printf("[leavechat] Leave chat from unauthorized group %v", update.Message.Chat.ID)
					if err != nil {
						log.Printf("[leavechat] Error Leave chat from unauthorized group %v", update.Message.Chat.ID)
					}
				} else if member.IsBot && member.UserName != app.Config.BotUsername {
					// Kick other bot
					_, err := bot.KickChatMember(tg.KickChatMemberConfig{
						ChatMemberConfig: tg.ChatMemberConfig{
							ChatID: update.Message.Chat.ID,
							UserID: member.ID,
						},
					})
					log.Printf("[kickbot] Kick bot @%s", member.UserName)
					if err != nil {
						log.Printf("[kickbot] Error kick bot @%s :%v", member.UserName, err)
					}
				} else {
					// Send welcome message except itself
					if member.UserName != app.Config.BotUsername {
						text := fmt.Sprintf("Selamat datang [%s](tg://user?id=%d) 😊", member.FirstName, member.ID)
						msg := tg.NewMessage(update.Message.Chat.ID, text)
						msg.ParseMode = "markdown"

						log.Println("New chat members", member.FirstName, member.ID)

						bot.Send(msg)
					}
				}
			}

		case update.Message.Text != "":
			// Filter Group command
			m := update.Message.Text

			if i := strings.Index(m, "!"); i == 0 {
				s := strings.Split(m, " ")
				cs := strings.Replace(s[0], "!", "", 1)
				log.Printf("[command] %s", cs)

				// Handle Update
				c := commands.Command{
					Bot:     bot,
					Message: update.Message,
					DB:      app.DB,
					Config:  app.Config,
				}
				c.Handle(cs)
			} else {
				// TODO: if message not a command
				// Do nothing for now
			}

		case update.Message.Photo != nil:
			//TODO: Handle Photo message
			log.Println("New Photo Message")

		case update.Message.Sticker != nil:
			//TODO: Handle Sticker Message
			log.Println("New Sticker Message")

		}

	}
}
