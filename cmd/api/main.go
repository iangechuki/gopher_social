package main

import (
	"gopher_social/internal/db"
	"gopher_social/internal/env"
	"gopher_social/internal/store"
	"time"

	"go.uber.org/zap"
)

const version = "0.0.2"

//	@title	Gopher Social API

//	@description	API for GopherSocial : a social network for gophers
//	@termsOfService	http://swagger.io/terms/

//	@contact.name	API Support
//	@contact.url	http://www.swagger.io/support
//	@contact.email	support@swagger.io

//	@license.name	Apache 2.0
//	@license.url	http://www.apache.org/licenses/LICENSE-2.0.html

//	@BasePath	/v1

//
//	@securityDefinitions.apikey	ApiKeyAuth
//	@in							header
//	@name						Authorization

func main() {

	cfg := config{
		addr:   env.GetString("ADDR", ":8000"),
		apiURL: env.GetString("API_URL", "localhost:8081"),
		db: dbConfig{
			addr: env.GetString("DB_ADDR", "postgres://admin:adminpassword@localhost:5432/gopher_social?sslmode=disable"),

			maxOpenConns: env.GetInt("DB_MAX_OPEN_CONNS", 25),
			maxIdleConns: env.GetInt("DB_MAX_IDLE_CONNS", 25),
			maxIdleTime:  env.GetString("DB_MAX_IDLE_TIME", "15m"),
		},
		env: env.GetString("ENV", "development"),
		mail: mailConfig{
			exp: time.Hour * 24 * 3,
		},
		version: version,
	}
	//Logger
	logger := zap.Must(zap.NewProduction()).Sugar()
	defer logger.Sync()

	//Database
	db, err := db.New(cfg.db.addr, cfg.db.maxOpenConns, cfg.db.maxIdleConns, cfg.db.maxIdleTime)
	if err != nil {
		logger.Fatal(err)
	}
	defer db.Close()

	logger.Info("✅ Connected to database")

	store := store.NewPostgresStorage(db)
	app := application{
		config: cfg,
		store:  store,
		logger: logger,
	}
	mux := app.mount()

	logger.Fatal(app.run(mux))
}
