package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const (
	urlBase = "https://news.fzu.edu.cn/fdyw.htm"
)

type image struct {
	image string
	title string
}

type article struct {
	title     string
	date      string
	author    string
	readCount int
	content   []string
	image     []image
}

func imageGet(url string) (string, bool) {
	response, err := http.Get(url)
	if err != nil {
		log.Fatalln(err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Fatalln(err)
		}
	}(response.Body)
	if response.StatusCode == 404 {
		return "", false
	} else if response.StatusCode != 200 {
		log.Fatalf("Status code error: %d %s, url: %s\n", response.StatusCode, response.Status, url)
	}
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Fatalln(err)
	}
	return base64.StdEncoding.EncodeToString(body), true
}

func pathGet(url string, date string) (string, []string) {
	response, err := http.Get(url)
	if err != nil {
		log.Fatalln("#1:", err, "url: ", url)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Fatalln("#2: ", err, "url: ", url)
		}
	}(response.Body)
	if response.StatusCode != 200 {
		log.Fatalf("Status code error: %d %s, url: %s\n", response.StatusCode, response.Status, url)
	}

	htm, err := goquery.NewDocumentFromReader(response.Body)
	if err != nil {
		log.Fatalln("#3:", err)
	}
	nextPagePath, done := htm.Find("span.p_next.p_fun > a").Attr("href")
	if !done {
		log.Fatalln("Can't get next page url.")
	}
	var contents []string
	htm.Find(".list_main_content > ul > li").Each(func(i int, s *goquery.Selection) {
		if s.Find("span").Text() >= date {
			url, done := s.Find("a").Attr("href")
			if !done {
				log.Fatalln("Can't get context url.")
			}
			contents = append(contents, url)
		}
	})
	return nextPagePath, contents
}

func articleGet(url string) article {
	var article article

	response, err := http.Get(url)
	if err != nil {
		log.Fatalln(err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Fatalln(err)
		}
	}(response.Body)
	if response.StatusCode != 200 {
		log.Fatalf("Status code error: %d %s, url: %s\n", response.StatusCode, response.Status, url)
	}

	htm, err := goquery.NewDocumentFromReader(response.Body)
	if err != nil {
		log.Fatalln(err)
	}
	htm.Find(".detail_main_content > p").Each(func(i int, s *goquery.Selection) {
		article.title += s.Text()
	})
	article.date = htm.Find("#fbsj").Text()
	article.author = htm.Find("#author").Text()
	article.readCount = countGet(url)
	htm.Find(".v_news_content > p").Each(func(i int, s *goquery.Selection) {
		if len(s.Find("img").Nodes) != 0 {
			var image image
			path, done := s.Find("img").Attr("src")
			if !done {
				log.Fatalln("Can't find src!")
			}
			image.image, done = imageGet(urlGet(url, path))
			if done{
				if len(s.Next().Find("span").Nodes) != 0 {
					image.title = s.Next().Find("span").Text()
				}
				article.image = append(article.image, image)
			}
		} else if strings.Trim(s.Text(), "  ") != "" {
			article.content = append(article.content, s.Text())
			// todo: s.text() trim (" ")
		}
	})

	return article
}

func urlGet(url string, path string) string {
	if path[0] == '/' {
		root := strings.SplitAfterN(url, "//", 2)[0] + strings.SplitN(strings.SplitN(url, "//", 2)[1], "/", 2)[0]
		return root + path
	} else {
		url = strings.TrimRightFunc(url, func(r rune) bool {
			return r != '/'
		})
		return url + path
	}
}

func countGet(url string) int {
	clickId := regexp.MustCompile(`/([0-9]*)\.htm`).FindStringSubmatch(url)[1]
	path := "/system/resource/code/news/click/dynclicks.jsp?clickid=" + clickId + "&owner=1744991928&clicktype=wbnews"
	response, err := http.Get(urlGet(url, path))
	if err != nil {
		log.Fatalln(err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Fatalln(err)
		}
	}(response.Body)
	if response.StatusCode != 200 {
		log.Fatalf("Status code error: %d %s, url: %s\n", response.StatusCode, response.Status, url)
	}
	body, err := ioutil.ReadAll(response.Body)
	count, err := strconv.Atoi(string(body))
	if err != nil {
		log.Fatalln(err)
	}
	return count
}

func databaseChange(article article, db *sqlx.DB) {
	var p []uint8
	err := db.Get(&p, "SELECT image FROM fdyw_articles WHERE title=? AND date=?", article.title, article.date)
	if err == sql.ErrNoRows {
		articleInsert(article, db)
	} else if err != nil {
		log.Fatalln(err)
	} else {
		var id int
		_ = db.Get(&id, "SELECT id FROM fdyw_articles WHERE title=? AND date=?", article.title, article.date)
		articleUpdate(article, db, id)
		if string(p) != "null" {
			var ids []int
			err = json.Unmarshal(p, &ids)
			if err != nil {
				log.Fatalln(err)
			}
			imageUpdate(article.image, db, ids)
		}
	}
}

func articleInsert(article article, db *sqlx.DB) {
	content, err := json.Marshal(article.content)
	if err != nil {
		log.Fatalln(err)
	}
	var images []int64
	for _, v := range article.image {
		result, err := db.Exec("INSERT INTO fdyw_image (title, image) VALUES (?, ?)", v.title, v.image)
		if err != nil {
			log.Fatalln(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			log.Fatalln(err)
		}
		images = append(images, id)
	}
	imagesStr, err := json.Marshal(images)
	_, err = db.Exec("INSERT INTO fdyw_articles ( title, date, author, count, content, image ) VALUES ( ?, ?, ?, ?, ?, ? )",
		article.title, article.date, article.author, article.readCount, string(content), string(imagesStr))
	if err != nil {
		log.Fatalln(err)
	}
}

func articleUpdate(article article, db *sqlx.DB, id int) {
	content, err := json.Marshal(article.content)
	if err != nil {
		log.Fatalln(err)
	}
	_, err = db.Exec("UPDATE fdyw_articles SET author=?, count=?, content=? WHERE id=?", article.author, article.readCount, content, id)
	if err != nil {
		log.Fatalln(err)
	}
}

func imageUpdate(images []image, db *sqlx.DB, id []int) {
	for i := range id{
		_, err := db.Exec("UPDATE fdyw_image SET title=?, image=? WHERE id=?", images[i].title, images[i].image, id[i])
		if err != nil {
			log.Fatalln(err)
		}
	}
}

func main() {
	dsn := "w2ol:@tcp(localhost:3306)/w2ol?charset=utf8&parseTime=True&loc=Local"
	db, err := sqlx.Open("mysql", dsn)
	if err != nil {
		log.Fatalln(err)
	}
	defer func(db *sqlx.DB) {
		err := db.Close()
		if err != nil {
			log.Fatalln(err)
		}
	}(db)
	db.SetMaxOpenConns(1000)
	db.SetMaxIdleConns(20)

	url := urlBase
	var path string
	var contexts []string
	wg := sync.WaitGroup{}
	for {
		path, contexts = pathGet(url, "2020-01-01")
		for _, path := range contexts {
			go func(url string, path string) {
				wg.Add(1)
				article := articleGet(urlGet(url, path))
				databaseChange(article, db)
				wg.Done()
			}(url, path)
		}
		url = urlGet(url, path)
		if len(contexts) < 40 {
			fmt.Println(url)
			fmt.Println(contexts)
			wg.Wait()
			break
		}
	}


	//article := articleGet("https://news.fzu.edu.cn/info/1002/11850.htm")
	//databaseChange(article, db)

	//var p []uint8
	//title := "福州大学召开甲型H1N1流感防治工作会议"
	//date := "2009-11-11"
	//err = db.Get(&p, "SELECT image FROM fdyw_articles WHERE title=? AND date=?", title, date)
	//if err != nil {
	//	log.Fatalln(err)
	//}
	//fmt.Println(p)

	//title := "中共福州大学梅努斯国际工程学院联合教育委员会党员大会顺利召开"
	//var p []uint8
	//err = db.Get(&p, "SELECT image FROM fdyw_articles WHERE title=? AND date=?", title, "2021-11-09")
	//if err == sql.ErrNoRows {
	//
	//} else if err != nil {
	//	log.Fatalln(err)
	//} else {
	//	var t []int
	//	_ = json.Unmarshal(p, &t)
	//	fmt.Println(t[0])
	//}
}
