package dbhelper

import (
	"database/sql"
	"fmt"
)

type DbConfig struct {
	Driver string
	Host   string
	Port   string
	User   string
	Pass   string
	DbName string
}

func GetConnection(config DbConfig) (db *sql.DB) {
	psqlInfo := fmt.Sprintf("host=%s port=%s user=%s "+
		"password=%s dbname=%s sslmode=disable",
		config.Host, config.Port, config.User, config.Pass, config.DbName)

	db, err := sql.Open(config.Driver, psqlInfo)
	if err != nil {
		panic(err)
	}

	return db
}
