package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	wg = sync.WaitGroup{}
)

type comments []comment

type (
	rawJson struct {
		//Code    int    `json:"code"`
		//Message string `json:"message"`
		//Ttl     int    `json:"ttl"`
		Data struct {
			//Page struct {
			//	Num    int `json:"num"`
			//	Size   int `json:"size"`
			//	Count  int `json:"count"`
			//	ACount int `json:"acount"`
			//} `json:"page"`
			//Config struct {
			//	ShowAdmin  int  `json:"showadmin"`
			//	ShowEntry  int  `json:"showentry"`
			//	ShowFloor  int  `json:"showfloor"`
			//	ShowTopic  int  `json:"showtopic"`
			//	ShowUpFlag bool `json:"show_up_flag"`
			//	ReadOnly   bool `json:"read_only"`
			//	ShowDelLog bool `json:"show_del_log"`
			//} `json:"config"`
			Replies []replay `json:"replies"`
		} `json:"data"`
	}

	replay struct {
		RpID    uint64   `json:"rpid"`
		MId     uint64   `json:"mid"`
		Root    uint64   `json:"root"`
		Parent  uint64   `json:"parent"`
		CTime   uint64   `json:"ctime"`
		Like    uint     `json:"like"`
		Replies []replay `json:"replies"`
		Content struct {
			Message string `json:"message"`
		} `json:"content"`
		Member struct {
			UName     string `json:"uname"`
			LevelInfo struct {
				CurrentLevel int `json:"current_level"`
			} `json:"level_info"`
		} `json:"member"`
	}

	comment struct {
		id        uint64
		root      uint64
		parent    uint64
		time      string
		like      uint
		message   string
		replies   []comment
		userId    uint64
		userName  string
		userLevel int
	}
)

func (comments *comments) UnmarshalJSON(data []byte) error {
	var rawJson rawJson
	err := json.Unmarshal(data, &rawJson)
	errCheck(err)
	*comments = marshalRawJson(rawJson.Data.Replies)
	return nil
}

func marshalRawJson(replies []replay) (comments comments) {
	for _, replay := range replies {
		var comment comment
		comment.id = replay.RpID
		comment.root = replay.Root
		comment.parent = replay.Parent
		comment.time = time.Unix(int64(replay.CTime), 0).Format("2006-01-02 15:04:05")
		comment.like = replay.Like
		comment.message = replay.Content.Message

		comment.userId = replay.MId
		comment.userName = replay.Member.UName
		comment.userLevel = replay.Member.LevelInfo.CurrentLevel

		comment.replies = marshalRawJson(replay.Replies)
		comments = append(comments, comment)
	}
	return
}

func errCheck(err error) {
	if err != nil {
		log.Fatalln(err)
	}
}

func commentGet(page int, url string) (comments comments) {
	response, err := http.Get(url + strconv.Itoa(page))
	errCheck(err)
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		errCheck(err)
	}(response.Body)
	if response.StatusCode != 200 {
		log.Fatalln(response.StatusCode, response.Status)
	}
	readAll, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Fatalln(err)
	}
	err = json.Unmarshal(readAll, &comments)
	errCheck(err)
	return
}

func commentInsertDriver(comments2 comments, db *sqlx.DB) {
	for _, comment2 := range comments2 {
		if comment2.replies != nil {
			go func(comments3 comments) {
				wg.Add(1)
				commentInsertDriver(comments3, db)
				wg.Done()
			}(comment2.replies)
		}
		go func(comment3 comment) {
			wg.Add(1)
			commentInsert(comment3, db)
			wg.Done()
		}(comment2)
	}
}

func commentInsert(comment comment, db *sqlx.DB) {
	var id int
	err := db.Get(&id, "SELECT id FROM bilibili_comments WHERE id=?", comment.id)
	message := UnicodeEmojiCode(comment.message)
	if err == sql.ErrNoRows {
		_, err := db.Exec(
			"INSERT INTO bilibili_comments ( id, root, parent, date, `like`, message, userId, userName, userLevel ) VALUES ( ?, ?, ?, ?, ?, ?, ?, ?, ? )",
			comment.id, comment.root, comment.parent, comment.time, comment.like, message, comment.userId, comment.userName, comment.userLevel)
		errCheck(err)
	} else {
		_, err := db.Exec("UPDATE bilibili_comments SET root=?, parent=?, date=?, `like`=?, message=?, userId=?, userName=?, userLevel=? WHERE id=?",
			comment.root, comment.parent, comment.time, comment.like, message, comment.userId, comment.userName, comment.userLevel, comment.id)
		errCheck(err)
	}
}

func UnicodeEmojiDecode(s string) string {
	re := regexp.MustCompile("\\[[\\\\u0-9a-zA-Z]+\\]")
	reg := regexp.MustCompile("\\[\\\\u|]")
	src := re.FindAllString(s, -1)
	for i := 0; i < len(src); i++ {
		e := reg.ReplaceAllString(src[i], "")
		p, err := strconv.ParseInt(e, 16, 32)
		if err == nil {
			s = strings.Replace(s, src[i], string(rune(p)), -1)
		}
	}
	return s
}

func UnicodeEmojiCode(s string) string {
	ret := ""
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if len(string(rs[i])) == 4 {
			u := `[\u` + strconv.FormatInt(int64(rs[i]), 16) + `]`
			ret += u

		} else {
			ret += string(rs[i])
		}
	}
	return ret
}

func databaseConnect() *sqlx.DB {
	dsn := "w2ol:@tcp(localhost:3306)/w2ol?charset=utf8mb4&parseTime=True&loc=Local"
	db, err := sqlx.Open("mysql", dsn)
	if err != nil {
		log.Fatalln(err)
	}
	db.SetMaxOpenConns(1000)
	db.SetMaxIdleConns(20)
	return db
}

func commentGetBegin(oid int, db *sqlx.DB, wait func()) {
	url := "https://api.bilibili.com/x/v2/reply?jsonp=jsonp&type=1&oid=" + strconv.Itoa(oid) + "&mode=2&pn="
	page := pageCountGet(url, wait)
	for i := 0; ; i++ {
		wait()
		comments := commentGet(i, url)
		go func() {
			wg.Add(1)
			commentInsertDriver(comments, db)
			wg.Done()
		}()
		if comments == nil {
			break
		}
		if i > page {
			page = i
		}
		progressBar(i, page)
	}
	wg.Wait()
	err := db.Close()
	errCheck(err)
}

func pageCountGet(url string, wait func()) int {
	a, b := 1, 32768
	temp := 0
	progressBar(0,a)
	if commentGet(a, url) == nil {
		return 0
	}
	wait()
	if commentGet(b, url) != nil {
		return -1
	}
	wait()
	for {
		temp = (a + b) / 2
		if commentGet(temp, url) != nil {
			a = temp
		} else {
			b = temp
		}
		progressBar(0,a)
		if b-a <= 1 {
			return a
		}
		wait()
	}
}

func delayMs(avr uint, rge uint) error {
	if rge > avr {
		return errors.New("range is too large")
	}

	rand.Seed(time.Now().UnixNano())
	time.Sleep(time.Millisecond * time.Duration(rand.Intn(int(rge*2))+int(avr-rge)))
	return nil
}

func progressBar(done int, all int) {
	progress := float32(done) / float32(all) * 100
	fmt.Print(progress)
	var s = []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉", "█"}
	for i := 0; i < 100; i++ {
		fmt.Print("\b")
	}
	for i := 0; i < 50; i++ {
		if progress >= 2 {
			progress -= 2
			fmt.Print("█")
		} else if progress == 0 {
			fmt.Print(" ")
		} else {
			fmt.Print(s[int(progress/.25)])
			progress = 0
		}
	}
	fmt.Printf("▏ %d/%d  %.2f%c", done, all, float32(done) / float32(all) * 100, '%')
}

func main() {

	db := databaseConnect()
	commentGetBegin(54737593, db, func() {
		err := delayMs(5000, 2500)
		errCheck(err)
	})

	//for i := 1; ; i++ {
	//	comments := commentGet(1)
	//	go func() {
	//		wg.Add(1)
	//		commentInsertDriver(comments, db)
	//		wg.Done()
	//	}()
	//	if comments == nil {
	//		break
	//	}
	//}
	//wg.Wait()

	//for i := 0; ; i++ {
	//	comments := commentGet(i)
	//	if comments != nil {
	//
	//	}
	//}

	//proxy, err := url.Parse("http://121.43.190.89:3128/")
	//errCheck(err)
	//client := http.Client{
	//	Transport:     &http.Transport{
	//		Proxy: http.ProxyURL(proxy),
	//		MaxIdleConnsPerHost: 10,
	//		ResponseHeaderTimeout: time.Second * time.Duration(5),
	//	},
	//	Timeout: time.Second * 10,
	//}
	//response, err := client.Get(url_)
	//defer func(Body io.ReadCloser) {
	//	err := Body.Close()
	//	errCheck(err)
	//}(response.Body)
	//fmt.Println(response.TransferEncoding)
	//fmt.Println(response.Header)
	//all, err := ioutil.ReadAll(response.Body)
	//errCheck(err)
	//var body comments
	//err = json.Unmarshal(all, &body)
	//fmt.Println(body)
}
