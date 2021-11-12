package main

import (
	"Task2/bili"
	"github.com/jmoiron/sqlx"
	"log"
)

func main() {
	dsn := "w2ol:@tcp(localhost:3306)/w2ol?charset=utf8mb4&parseTime=True&loc=Local"
	db, err := sqlx.Open("mysql", dsn)
	if err != nil {
		log.Fatalln(err)
	}
	db.SetMaxOpenConns(1000)
	db.SetMaxIdleConns(20)

	crawler := bili.Crawler{
		Database: db,
		BVId:     "BV1f4411M7QC",
		Average:  5000,
		Range:    2500,
	}
	err = crawler.Go()
	if err != nil {
		return
	}
}
